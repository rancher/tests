package certificates

import "github.com/sirupsen/logrus"

// VerifyCertificateRotation verifies if the certificates have been rotated by comparing the certificates before and after rotation.
func VerifyCertificateRotation(oldCertificates, newCertificates map[string]map[string]string) bool {
	isRotated := true
	for nodeID := range oldCertificates {
		for certType := range oldCertificates[nodeID] {
			if oldCertificates[nodeID][certType] == newCertificates[nodeID][certType] {
				logrus.Warningf("%s %s was not updated: %s", nodeID, certType, oldCertificates[nodeID][certType])
				isRotated = false
			}
		}
	}

	return isRotated
}
