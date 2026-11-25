package github

import "testing"

// #168: GitHub reports the manifest's emails permission as email_addresses.
func TestMissingAcceptsEmailAddresses(t *testing.T) {
	in := Installation{
		Permissions: map[string]string{"contents": "write", "pull_requests": "write", "issues": "write", "checks": "write",
			"metadata": "read", "email_addresses": "read"},
		Events: NewManifest("", "", "", false).DefaultEvents,
	}
	if got := in.Missing(); len(got) != 0 {
		t.Fatalf("Missing() = %v, want nothing", got)
	}
}
