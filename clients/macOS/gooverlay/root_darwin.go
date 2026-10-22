// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// loft overlay for macOS 10.13, against the go.mod toolchain (Go 1.26).
// Stdlib calls SecTrustEvaluateWithError (10.14) then
// SecTrustCopyCertificateChain (12.0). This copy evaluates with
// SecTrustEvaluate and walks the chain with GetCertificateCount/AtIndex.
// Keep loadSystemRoots in this file: Go 1.26 defines it here.

package x509

import (
	"crypto/x509/internal/macos"
	"errors"
)

func (c *Certificate) systemVerify(opts *VerifyOptions) (chains [][]*Certificate, err error) {
	certs := macos.CFArrayCreateMutable()
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

	policies := macos.CFArrayCreateMutable()
	defer macos.ReleaseCFArray(policies)
	sslPolicy, err := macos.SecPolicyCreateSSL(opts.DNSName)
	if err != nil {
		return nil, err
	}
	macos.CFArrayAppendValue(policies, sslPolicy)

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

	result, err := macos.SecTrustEvaluate(trustObj)
	if err != nil {
		return nil, err
	}
	if !macos.TrustResultOK(result) {
		return nil, UnknownAuthorityError{Cert: c}
	}

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

func loadSystemRoots() (*CertPool, error) {
	return &CertPool{systemPool: true}, nil
}
