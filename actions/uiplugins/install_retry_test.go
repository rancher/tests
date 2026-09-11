package uiplugins

import (
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type transientTestError struct {
	reason metav1.StatusReason
}

func (e *transientTestError) Error() string {
	return "an error on the server (\"" + string(e.reason) + "\") has prevented the request from succeeding"
}

// Status ensures value implements apierrors.APIStatus so it classifies as a catalog server error.
func (e *transientTestError) Status() metav1.Status {
	return metav1.Status{
		Status: metav1.StatusFailure,
		Code:   500,
		Reason: e.reason,
	}
}

var _ apierrors.APIStatus = (*transientTestError)(nil)

func TestIsTransientInstallError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error is not retried",
			err:  nil,
			want: false,
		},
		{
			name: "generic non-status error is not retried",
			err:  errors.New("some validation failure"),
			want: false,
		},
		{
			name: "unknown-reason server error is retried",
			err:  &transientTestError{reason: metav1.StatusReasonUnknown},
			want: true,
		},
		{
			name: "internal server error is retried",
			err:  apierrors.NewInternalError(errors.New("boom")),
			want: true,
		},
		{
			name: "server timeout is retried",
			err:  apierrors.NewServerTimeout(schema.GroupResource{Group: "catalog.cattle.io", Resource: "apps"}, "get", 1),
			want: true,
		},
		{
			name: "too many requests is retried",
			err:  apierrors.NewTooManyRequests("rate limited", 5),
			want: true,
		},
		{
			name: "not found is not retried",
			err:  apierrors.NewNotFound(schema.GroupResource{Group: "catalog.cattle.io", Resource: "clusterrepos"}, "rancher-ui-plugins"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientInstallError(tt.err); got != tt.want {
				t.Errorf("isTransientInstallError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
