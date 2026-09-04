package monitoring

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveImageReference(t *testing.T) {
	tests := []struct {
		name            string
		defaultRegistry string
		image           string
		want            string
	}{
		{
			name:            "registry prefixes hostless image",
			defaultRegistry: "reg.example.com:5000",
			image:           "traefik:v3.7.12",
			want:            "reg.example.com:5000/traefik:v3.7.12",
		},
		{
			name:            "empty registry leaves image untouched",
			defaultRegistry: "",
			image:           "traefik:v3.7.12",
			want:            "traefik:v3.7.12",
		},
		{
			name:            "image with explicit registry host is untouched",
			defaultRegistry: "reg.example.com:5000",
			image:           "docker.io/library/traefik:v3.7.12",
			want:            "docker.io/library/traefik:v3.7.12",
		},
		{
			name:            "localhost registry host is untouched",
			defaultRegistry: "reg.example.com:5000",
			image:           "localhost:5000/traefik:v3.7.12",
			want:            "localhost:5000/traefik:v3.7.12",
		},
		{
			name:            "trailing slash in registry yields single-slash join",
			defaultRegistry: "reg.example.com:5000/",
			image:           "traefik:v3.7.12",
			want:            "reg.example.com:5000/traefik:v3.7.12",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ResolveImageReference(tt.defaultRegistry, tt.image))
		})
	}
}
