package main

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net"
	"net/http"
	"time"
)

// Routes incoming requests — CONNECT goes to tunnel, everything else is forwarded as HTTP
func handleRequest(w http.ResponseWriter, r *http.Request, caCert *x509.Certificate, caKey crypto.PrivateKey) {
	if r.Method == "CONNECT" {
		handleTunnel(w, r, caCert, caKey)
		return
	}
	log.Println(r.Method, r.URL)

	// Remove gzip encoding so response body arrives as plain text
	r.Header.Del("Accept-Encoding")
	// Buffer the request body — forwarding consumes it, but sendToAPI needs it too
	reqBody := readBody(r)
	start := time.Now()
	// Forward request to real destination and get response
	res, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	duration := time.Since(start)
	defer res.Body.Close()
	// Read body once — needed by both the client response and sendToAPI
	resBody, _ := io.ReadAll(res.Body)
	res.Body = io.NopCloser(bytes.NewReader(resBody))
	// Copy all response headers to the client
	for key, values := range res.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(res.StatusCode)
	w.Write(resBody)
	r.Body = io.NopCloser(bytes.NewReader(reqBody))
	sendToAPI(r, res, duration, false)
}

// Handles HTTPS CONNECT — performs TLS MITM: decrypts browser traffic, reads it, re-encrypts to real server
func handleTunnel(w http.ResponseWriter, r *http.Request, caCert *x509.Certificate, caKey crypto.PrivateKey) {
	// Open raw TCP connection to destination (e.g. example.com:443)
	conn, err := net.Dial("tcp", r.URL.Host)
	if err != nil {
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	defer conn.Close()

	// Take raw TCP control of the client connection away from Go's HTTP server
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Generate a per-domain cert signed by our local CA so the browser trusts us
	cert, err := generateCert(r.URL.Hostname(), caCert, caKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Wrap browser-facing connection as TLS server — we present the generated cert
	tlsBrowser := tls.Server(clientConn, &tls.Config{
		Certificates: []tls.Certificate{*cert},
	})
	// Wrap server-facing connection as TLS client — we verify the real server's cert
	tlsServer := tls.Client(conn, &tls.Config{
		ServerName: r.URL.Hostname(),
	})

	// Tell the browser the tunnel is ready — triggers TLS handshake on client side
	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Complete TLS handshake with browser
	if err = tlsBrowser.Handshake(); err != nil {
		log.Println("TLS handshake with browser failed:", err)
		return
	}
	// Complete TLS handshake with real server
	if err = tlsServer.Handshake(); err != nil {
		log.Println("TLS handshake with server failed:", err)
		return
	}

	// Browsers reuse one TLS connection for many requests (keep-alive), so keep reading until it closes
	browserReader := bufio.NewReader(tlsBrowser)
	serverReader := bufio.NewReader(tlsServer)
	for {
		start := time.Now()
		// Parse the decrypted HTTP request sent by the browser through the TLS tunnel
		req, err := http.ReadRequest(browserReader)
		if err != nil {
			if err != io.EOF {
				log.Println("Error reading request from client: ", err)
			}
			return
		}
		// Reconstruct full URL — inside a tunnel the browser only sends the path, not the full URL
		log.Println(req.Method, "https://"+r.URL.Hostname()+req.URL.String())
		req.Header.Del("Accept-Encoding")
		// Buffer the request body — writing to the server consumes it, but sendToAPI needs it too
		reqBody := readBody(req)
		// Forward the request to the real server through our TLS client connection
		if err = req.Write(tlsServer); err != nil {
			log.Println("Error writing to server: ", err)
			return
		}
		// Read the real server's response
		res, err := http.ReadResponse(serverReader, req)
		if err != nil {
			log.Println("Error reading response from server: ", err)
			return
		}
		// Read body once — needed by both the browser response and sendToAPI
		resBody, _ := io.ReadAll(res.Body)
		res.Body.Close()
		res.Body = io.NopCloser(bytes.NewReader(resBody))
		// Send the response back to the browser
		if err = res.Write(tlsBrowser); err != nil {
			log.Println("Error writing back to client: ", err)
			return
		}
		// Reconstruct full HTTPS URL before sending to API — tunnel only gives us the path
		req.URL.Host = r.URL.Hostname()
		req.URL.Scheme = "https"
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
		res.Body = io.NopCloser(bytes.NewReader(resBody))
		sendToAPI(req, res, time.Since(start), true)
		// Stop if either side asked to close the connection
		if req.Close || res.Close {
			return
		}
	}
}

// Reads the full request body and puts a fresh copy back so the request can still be forwarded
func readBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(b))
	return b
}
