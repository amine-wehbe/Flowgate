package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"os"
	"time"
)

// Loads the mkcert root CA cert and private key from disk as a tls.Certificate
func loadCA() (tls.Certificate, error) {
	// No fallback path — the CA location must be provided explicitly (run `mkcert -CAROOT` to find it)
	caRoot := os.Getenv("MKCERT_CAROOT")
	if caRoot == "" {
		return tls.Certificate{}, errors.New("MKCERT_CAROOT is not set")
	}
	cert, err := tls.LoadX509KeyPair(caRoot+"/rootCA.pem", caRoot+"/rootCA-key.pem")
	if err != nil {
		return tls.Certificate{}, err
	}
	return cert, nil
}

// Parses raw DER bytes from the loaded cert into an x509.Certificate usable for signing
func parseCA(c tls.Certificate) (*x509.Certificate, error) {
	cert, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return nil, err
	}
	return cert, nil
}

// Dynamically generates a TLS cert for the given host, signed by our local CA
func generateCert(host string, caCert *x509.Certificate, caKey crypto.PrivateKey) (*tls.Certificate, error) {
	// Generate a fresh RSA key pair for this certificate
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	// Random 128-bit serial — certs from the same CA must have unique serials or browsers may reject them
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	// Define certificate attributes — valid for 24h, bound to this specific host
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{host},
	}
	// Sign the template with the CA key — produces raw DER-encoded certificate bytes
	der, err := x509.CreateCertificate(rand.Reader, &template, caCert, &rsaKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: rsaKey}, nil
}
