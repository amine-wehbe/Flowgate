package main

import (
	"log"
	"net/http"
)

func main() {
	// Load CA cert+key from mkcert's local store — used to sign per-domain TLS certs at runtime
	c, err := loadCA()
	if err != nil {
		log.Fatal("Error loading CA certificate: ", err)
	}
	// Parse raw cert bytes into x509 struct needed by generateCert
	cert, err := parseCA(c)
	if err != nil {
		log.Fatal("Error parsing CA certificate: ", err)
	}

	log.Println("Flowgate proxy listening on :8080")
	// Use a closure to pass CA cert+key into handleRequest without changing its http.HandlerFunc signature
	err = http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleRequest(w, r, cert, c.PrivateKey)
	}))
	if err != nil {
		log.Fatal(err)
	}
}
