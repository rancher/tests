package prime

const (
	sccRegistrationSecretName = "scc-registration"
)

func CreateSCCRegistrationSecret(sccNamespace, regCode, registrationType string) (map[string]any, error) {
	secret := map[string]any{
		"metadata": map[string]any{
			"namespace": sccNamespace,
			"name":      sccRegistrationSecretName,
		},
		"data": map[string]any{
			"regCode":          regCode,
			"registrationType": registrationType,
		},
	}

	return secret, nil
}
