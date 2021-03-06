package main

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"

	"astuart.co/vpki"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/apis/extensions/v1beta1"
)

const (
	// GenCertsAnnotation specifies that certificates should be generated by this process
	GenCertsAnnotation = "kubernetes.io/tls-vault"
)

func (ctr *certer) addTLSSecrets(ing *v1beta1.Ingress) (*v1beta1.Ingress, error) {
	annotAllows := len(ing.Spec.TLS) > 0 && ing.Annotations[GenCertsAnnotation] != ""

	log.Info(ing.Spec.TLS)
	log.Info(ing.Annotations[GenCertsAnnotation])

	if !(annotAllows || *forceTLS) {
		return nil, fmt.Errorf("No ingress to update")
	}

	log.WithFields(log.Fields{
		"ingress":   fmt.Sprintf("%s/%s", ing.Namespace, ing.Name),
		"namespace": ing.Namespace,
	}).Infof("Issuing certificates for %s", ing.Name)

	err := ctr.addNeededHosts(ing)
	if err != nil {
		return nil, err
	}

	for _, tls := range ing.Spec.TLS {
		if len(tls.Hosts) < 1 {
			continue
		}

		var sec *v1.Secret
		var newSec bool

		namespace := ctr.namespace
		if strings.TrimSpace(namespace) == "" {
			namespace = ing.Namespace
		}

		logger := log.WithFields(log.Fields{
			"namespace": namespace,
			"ingress":   fmt.Sprintf("%s/%s", ing.Namespace, ing.Name),
			"secret":    tls.SecretName,
		})

		sec, err = ctr.api.Secrets(namespace).Get(tls.SecretName)
		if err != nil {
			logger.Infof("secret %q not found; creating new secret", tls.SecretName)

			newSec = true
			sec = &v1.Secret{
				ObjectMeta: v1.ObjectMeta{
					Namespace: namespace,
					Name:      tls.SecretName,
				},
				Data: map[string][]byte{},
			}
		}

		// Check whether or not we need to reissue
		if bs, ok := sec.Data["tls.crt"]; ok {
			c, err := x509.ParseCertificate(bs)
			if err == nil && time.Now().Before(c.NotAfter) {
				continue
			}
		}

		var keyPair *vpki.RawPair

		switch certer := ctr.c.(type) {
		case *vpki.Client:
			csr := &x509.CertificateRequest{
				DNSNames: tls.Hosts,
				Subject: pkix.Name{
					CommonName: tls.Hosts[0],
				},
			}

			logger.Debug("Using certificate request %#v\n", csr)

			keyPair, err = certer.GenCert(csr)
		default:
			keyPair, err = vpki.RawCert(certer, tls.Hosts[0])
		}

		if err != nil {
			return nil, fmt.Errorf("error getting raw certificate for secret %s: %s", tls.SecretName, err)
		}

		logger.Debug(keyPair.Public)

		sec.Data["tls.key"] = keyPair.Private
		sec.Data["tls.crt"] = keyPair.Public
		var op string

		if newSec {
			op = "creating"
			sec, err = ctr.api.Secrets(namespace).Create(sec)
		} else {
			op = "updating"
			sec, err = ctr.api.Secrets(namespace).Update(sec)
		}

		if err != nil {
			return nil, fmt.Errorf("Error %s secret %s: %s", op, sec.Name, err)
		}
	}

	return ing, nil
}

func (ctr *certer) addNeededHosts(ing *v1beta1.Ingress) error {
	changed := modifySpec(&ing.Spec)
	if !changed {
		return nil
	}

	i, err := ctr.api.Ingresses(ing.Namespace).Update(ing)
	if err != nil {
		return fmt.Errorf("Error updating ingress %s/%s: %s", ing.Namespace, ing.Name, err)
	}

	*ing = *i

	return nil
}

func modifySpec(spec *v1beta1.IngressSpec) bool {
	// Generate new TLS cert records if we're set to override all tls records
	if spec.TLS == nil {
		spec.TLS = []v1beta1.IngressTLS{}
	}

	neededEntries := missingHosts(spec.Rules, spec.TLS)

	if len(neededEntries) == 0 {
		return false
	}

	for _, host := range neededEntries {
		spec.TLS = append(spec.TLS, v1beta1.IngressTLS{
			Hosts:      []string{host},
			SecretName: host + ".tls",
		})
	}

	return true
}

func missingHosts(rules []v1beta1.IngressRule, tls []v1beta1.IngressTLS) []string {
	hosts := []string{}
	if len(rules) != len(tls) {
		return hosts
	}

	// Map all the hosts by name
	m := map[string]struct{}{}
	for _, rule := range rules {
		m[rule.Host] = struct{}{}
	}

	// Delete hosts as tls records are found that claim to cover
	for _, t := range tls {
		for _, host := range t.Hosts {
			delete(m, host)
		}
	}

	for h := range m {
		hosts = append(hosts, h)
	}

	return hosts
}
