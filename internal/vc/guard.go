package vc

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/vmware/govmomi/vim25/soap"
)

// allowed lists every SOAP method rightsizer may invoke. Anything else is
// rejected before it leaves the process, so the tool can never change vCenter.
var allowed = map[string]bool{
	"RetrieveServiceContent":       true,
	"Login":                        true,
	"Logout":                       true,
	"CurrentTime":                  true,
	"RetrieveProperties":           true,
	"RetrievePropertiesEx":         true,
	"ContinueRetrievePropertiesEx": true,
	"CancelRetrievePropertiesEx":   true,
	"CreateContainerView":          true,
	"DestroyView":                  true,
	"QueryPerf":                    true,
	"QueryPerfCounter":             true,
	"QueryAvailablePerfMetric":     true,
	"QueryPerfProviderSummary":     true,
	// Datastore search starts a task that only lists files.
	"SearchDatastoreSubFolders_Task": true,
}

type ReadOnlyError struct{ Method string }

func (e *ReadOnlyError) Error() string {
	return fmt.Sprintf("blocked non read-only vSphere call %q", e.Method)
}

type guard struct{ next soap.RoundTripper }

func (g guard) RoundTrip(ctx context.Context, req, res soap.HasFault) error {
	m := methodName(req)
	if !allowed[m] {
		return &ReadOnlyError{Method: m}
	}
	return g.next.RoundTrip(ctx, req, res)
}

func methodName(req soap.HasFault) string {
	t := reflect.TypeOf(req)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return strings.TrimSuffix(t.Name(), "Body")
}
