package servers

import (
	"strings"
	"testing"
)

func TestValidateFormAcceptsValidServer(t *testing.T) {
	validated, errs := ValidateForm(FormInput{
		Name:               "edge-1",
		Host:               "10.0.0.5",
		Port:               "22",
		AuthMode:           AuthModePassword,
		Username:           "root",
		Tags:               "prod, edge",
		CredentialStrategy: CredentialStrategyRuntime,
	})

	if errs.HasAny() {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}
	if validated.Server.Name != "edge-1" {
		t.Fatalf("Name = %q", validated.Server.Name)
	}
	if validated.Server.Port != 22 {
		t.Fatalf("Port = %d, want 22", validated.Server.Port)
	}
	if len(validated.Server.Tags) != 2 {
		t.Fatalf("Tags = %#v, want 2 entries", validated.Server.Tags)
	}
}

func TestValidateFormRejectsInvalidHost(t *testing.T) {
	_, errs := ValidateForm(FormInput{
		Name:               "bad-host",
		Host:               "not a host",
		Port:               "22",
		AuthMode:           AuthModePassword,
		Username:           "root",
		CredentialStrategy: CredentialStrategyRuntime,
	})

	if !errs.HasAny() {
		t.Fatal("expected validation errors")
	}
	if _, ok := errs["host"]; !ok {
		t.Fatalf("expected host error, got %#v", errs)
	}
}

func TestValidateFormPreservesStandaloneValidationMessages(t *testing.T) {
	base := FormInput{
		Name:               "edge-1",
		Host:               "10.0.0.5",
		Port:               "22",
		AuthMode:           AuthModePassword,
		Username:           "root",
		CredentialStrategy: CredentialStrategyRuntime,
	}

	withLongTag := base
	withLongTag.Tags = strings.Repeat("x", 41)
	_, errs := ValidateForm(withLongTag)
	if got, want := errs["tags"], "Each label must be 40 characters or fewer"; got != want {
		t.Errorf("tags error = %q, want %q", got, want)
	}

	withBadReference := base
	withBadReference.CredentialStrategy = CredentialStrategyExternalRef
	withBadReference.CredentialRef = "missing-equals"
	_, errs = ValidateForm(withBadReference)
	if got, want := errs["credential_ref"], `Credential reference segment "missing-equals" must use key=value`; got != want {
		t.Errorf("credential_ref error = %q, want %q", got, want)
	}
}
