// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// loft overlay for macOS 10.13, against the go.mod toolchain (Go 1.26).
// Stdlib calls SecTrustEvaluateWithError (10.14) then
// SecTrustCopyCertificateChain (12.0). This copy evaluates with
// SecTrustEvaluate and walks the chain with GetCertificateCount/AtIndex.
// Keep loadSystemRoots in this file: Go 1.26 defines it here.
//
// What gooverlay is: this directory is not a Go package of its own. Its files
// replace same-named files of the Go standard library at build time through
// `go build -overlay` (see README.md), so the macOS client binary starts on
// macOS 10.13 High Sierra. This file replaces crypto/x509/root_darwin.go: it
// asks macOS to check a server certificate chain. Its partner security.go
// replaces crypto/x509/internal/macos/security.go, the thin bindings to
// Security.framework used below. Both real packages keep their stdlib package
// docs, so these files carry only file headers.

package x509

import (
	"crypto/x509/internal/macos"
	"errors"
)

// systemVerify checks c against the macOS trust store (the system and user
// keychains) instead of Go's own root list, as the stdlib does on Darwin.
// It hands macOS the leaf plus opts.Intermediates and an SSL policy for
// opts.DNSName, evaluates the trust, then rebuilds the chain macOS chose as
// Go certificates. Host name and extended key usage are checked again in Go,
// matching the stdlib version. Unlike the stdlib it uses only API calls that
// exist on macOS 10.13 (SecTrustEvaluate and per-index chain access).
func (c *Certificate) systemVerify(opts *VerifyOptions) (chains [][]*Certificate, err error) {
	certs := macos.CFArrayCreateMutable() // 1. Leaf and intermediates go into a CFArray for Security.framework.
	defer macos.ReleaseCFArray(certs)
	leaf, err := macos.SecCertificateCreateWithData(c.Raw)
	if err != nil {
		return nil, errors.New("invalid leaf certificate")
	}
	macos.CFArrayAppendValue(certs, leaf)
	if opts.Intermediates != nil {
		for _, lc := range opts.Intermediates.lazyCerts {
			c, err := lc.getCert()
			if err != nil {
				return nil, err
			}
			sc, err := macos.SecCertificateCreateWithData(c.Raw)
			if err != nil {
				return nil, err
			}
			macos.CFArrayAppendValue(certs, sc)
		}
	}

	// 2. Build the SSL server policy; it carries the host name when one is set.
	policies := macos.CFArrayCreateMutable()
	defer macos.ReleaseCFArray(policies)
	sslPolicy, err := macos.SecPolicyCreateSSL(opts.DNSName)
	if err != nil {
		return nil, err
	}
	macos.CFArrayAppendValue(policies, sslPolicy)

	// 3. Create the trust object and, if the caller asked, pin the check time.
	trustObj, err := macos.SecTrustCreateWithCertificates(certs, policies)
	if err != nil {
		return nil, err
	}
	defer macos.CFRelease(trustObj)

	if !opts.CurrentTime.IsZero() {
		dateRef := macos.TimeToCFDateRef(opts.CurrentTime)
		defer macos.CFRelease(dateRef)
		if err := macos.SecTrustSetVerifyDate(trustObj, dateRef); err != nil {
			return nil, err
		}
	}

	// 4. Let macOS evaluate the chain. Only "proceed" or "unspecified" mean trusted.
	result, err := macos.SecTrustEvaluate(trustObj)
	if err != nil {
		return nil, err
	}
	if !macos.TrustResultOK(result) {
		return nil, UnknownAuthorityError{Cert: c}
	}

	// 5. Copy the chain macOS built (leaf first) back into Go certificates.
	chain := [][]*Certificate{{}}
	n := macos.SecTrustGetCertificateCount(trustObj)
	for i := 0; i < n; i++ {
		certRef := macos.SecTrustGetCertificateAtIndex(trustObj, i)
		if certRef == 0 {
			return nil, errors.New("x509: macos certificate verification internal error")
		}
		cert, err := exportCertificate(certRef)
		if err != nil {
			return nil, err
		}
		chain[0] = append(chain[0], cert)
	}
	if len(chain[0]) == 0 {
		// This should _never_ happen, but to be safe
		return nil, errors.New("x509: macos certificate verification internal error")
	}

	if opts.DNSName != "" {
		// If we have a DNS name, apply our own name verification
		if err := chain[0][0].VerifyHostname(opts.DNSName); err != nil {
			return nil, err
		}
	}

	// 6. Check extended key usage (server auth unless the caller said otherwise).
	keyUsages := opts.KeyUsages
	if len(keyUsages) == 0 {
		keyUsages = []ExtKeyUsage{ExtKeyUsageServerAuth}
	}

	// If any key usage is acceptable then we're done.
	for _, usage := range keyUsages {
		if usage == ExtKeyUsageAny {
			return chain, nil
		}
	}

	if !checkChainForKeyUsage(chain[0], keyUsages) {
		return nil, CertificateInvalidError{c, IncompatibleUsage, ""}
	}

	return chain, nil
}

// exportCertificate returns a *Certificate for a SecCertificateRef.
func exportCertificate(cert macos.CFRef) (*Certificate, error) {
	data, err := macos.SecCertificateCopyData(cert)
	if err != nil {
		return nil, err
	}
	return ParseCertificate(data)
}

// loadSystemRoots returns a marker pool: systemPool tells the verifier to call
// systemVerify (macOS) rather than read roots into memory. Go 1.26 expects
// this function in root_darwin.go, so it must stay in this file.
func loadSystemRoots() (*CertPool, error) {
	return &CertPool{systemPool: true}, nil
}
