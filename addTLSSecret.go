package main

import (
	"fmt"
	"log"

	"go.astuart.co/vpki"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/apis/extensions"
)

func (ctr *certer) addTLSSecrets(ing *extensions.Ingress) (*extensions.Ingress, error) {
	if len(ing.Spec.TLS) == 0 && !*forceTLS {
		return nil, fmt.Errorf("No ingresses to update")
	}

	var err error

	fmt.Println(ing.Name)

	ts := ing.Spec.TLS

	exTLS := make(map[string]*extensions.IngressTLS)

	for i, r := range ts {
		for _, h := range r.Hosts {
			exTLS[h] = &ts[i]
		}
	}

	if *forceTLS {
		// Add TLS defs if non-existent
		for _, rule := range ing.Spec.Rules {
			if _, ok := exTLS[rule.Host]; ok {
				// Early exit if existing TLS spec covering host
				continue
			}

			ts = append(ts, extensions.IngressTLS{
				Hosts:      []string{rule.Host},
				SecretName: rule.Host + ".tls",
			})
		}
	}

	success := []extensions.IngressTLS{}

	// Go through and update or create ingresses
	for _, tls := range ts {
		if len(tls.Hosts) < 1 {
			continue
		}

		var sec *api.Secret
		var newSec bool

		sec, err = ctr.api.Secrets(ing.Namespace).Get(tls.SecretName)
		if err != nil {
			newSec = true
			log.Println("Error getting secret", tls.SecretName, err)
			sec = &api.Secret{
				ObjectMeta: api.ObjectMeta{
					Namespace: ing.Namespace,
					Name:      tls.SecretName,
				},
				Data: map[string][]byte{},
			}
		}

		h := tls.Hosts[0]
		//TODO maybe do altnames here? The Ingress TLS struct is weirdly redundant.
		m, err := vpki.RawCert(ctr.c, h)
		if err != nil {
			log.Printf("Error getting raw certificate for %s: %s", h, err)
			continue
		}

		log.Println(string(m.Public))

		sec.Data["tls.key"] = m.Private
		sec.Data["tls.crt"] = m.Public
		var op string

		if newSec {
			op = "creating"
			sec, err = ctr.api.Secrets(ing.Namespace).Create(sec)
		} else {
			op = "updating"
			sec, err = ctr.api.Secrets(ing.Namespace).Update(sec)
		}

		if err != nil {
			log.Printf("Error %s secret %s: %s", op, sec.Name, err)
			continue
		}

		success = append(success, tls)
	}

	// Get the latest copy in case anything has changed
	ing, err = ctr.api.Ingress(ing.Namespace).Get(ing.Name)
	if err != nil {
		return nil, fmt.Errorf("Error getting latest update of ingress")
	}

	if len(success) != len(ing.Spec.TLS) {
		ing.Spec.TLS = success
		ing, err = ctr.api.Ingress(ing.Namespace).Update(ing)
		if err != nil {
			return nil, fmt.Errorf("Error updating ingress to remove failed certs")
		}
	}

	return ing, nil
}
