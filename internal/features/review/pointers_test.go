package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/engine/section"
)

func TestPointedSections(t *testing.T) {
	const doc = `# Pay

## 4. Security

Tokens follow [the data rules](#data). Retention is in [keys](SPEC.md#5-key-handling) and [elsewhere](other.md#audit-log).
See governance and
compliance. The error handling of the gateway is out of scope.

### Secrets

Rotated monthly.

## Governance and compliance

Admins skip MFA. See Audit log.

### Audit log

Each use is logged.

## Data

Orders.

## 5. Key handling

HSM.

## Error handling

Retries.

## Audit log

Second one.
`
	src := []byte(doc)
	d := section.Parse(src)
	got := []string{}
	for _, s := range pointedSections("pay/SPEC.md", src, d, section.At(d, []string{"Pay", "4. Security"})) {
		got = append(got, strings.Join(s.Path, " > "))
	}
	// The link to another file, the pointer of a pointed-to section, and a one-word title in
	// the text are no pointers.
	want := []string{"Pay > Governance and compliance", "Pay > Data", "Pay > 5. Key handling", "Pay > Error handling"}
	if !slices.Equal(got, want) {
		t.Errorf("pointed-to sections %q, want %q", got, want)
	}

	src = []byte("# Pay\n\n## Security\n\nThe data is encrypted.\n\n## Data\n\nOrders.\n")
	d = section.Parse(src)
	if p := pointedSections("SPEC.md", src, d, section.At(d, []string{"Pay", "Security"})); len(p) != 0 {
		t.Errorf("a one-word title in the text gave %d pointed-to sections", len(p))
	}
}
