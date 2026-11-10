package connection

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"net/http"
	"strings"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func TestValidateResource(t *testing.T) {
	t.Parallel()

	ok := &model.ConnectionResource{
		Name:       "pool",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node-1"},
		MaxPerUser: 16,
	}
	if err := validateResource(ok, 16); err != nil {
		t.Fatalf("valid resource: %v", err)
	}

	empty := *ok
	empty.Nodes = nil
	empty.NodeGroups = nil
	assertInvalid(t, validateResource(&empty, 16))

	over := *ok
	over.MaxPerUser = 17
	assertInvalid(t, validateResource(&over, 16))

	zero := *ok
	zero.MaxPerUser = 0
	assertInvalid(t, validateResource(&zero, 16))

	neg := *ok
	n := -1
	neg.Display = &n
	assertInvalid(t, validateResource(&neg, 16))

	rdp := *ok
	rdp.Protocol = contract.RDPProtocol
	d := 1
	rdp.Display = &d
	assertInvalid(t, validateResource(&rdp, 16))

	negTotal := *ok
	negTotal.MaxTotal = -1
	assertInvalid(t, validateResource(&negTotal, 16))

	totalLessThanUser := *ok
	totalLessThanUser.MaxPerUser = 5
	totalLessThanUser.MaxTotal = 3
	assertInvalid(t, validateResource(&totalLessThanUser, 16))

	validTotal := *ok
	validTotal.MaxPerUser = 2
	validTotal.MaxTotal = 10
	if err := validateResource(&validTotal, 16); err != nil {
		t.Fatalf("valid resource with maxTotal: %v", err)
	}

	xrdp := *ok
	xrdp.Protocol = contract.XRDPProtocol
	xrdp.Display = &d
	if err := validateResource(&xrdp, 16); err != nil {
		t.Fatalf("XRDP with fixed display should be valid: %v", err)
	}

	badVNC := *ok
	badVNC.VNCConfig = json.RawMessage(`"not-an-object"`)
	assertInvalid(t, validateResource(&badVNC, 16))

	goodVNC := *ok
	goodVNC.VNCConfig = json.RawMessage(`{"resolution":"1920x1080","startOptions":"-alwaysshared"}`)
	if err := validateResource(&goodVNC, 16); err != nil {
		t.Fatalf("valid vncConfig rejected: %v", err)
	}

	blankName := *ok
	blankName.Name = "   "
	assertInvalid(t, validateResource(&blankName, 16))

	longName := *ok
	longName.Name = strings.Repeat("a", maxResourceNameLen+1)
	assertInvalid(t, validateResource(&longName, 16))

	emptyNode := *ok
	emptyNode.Nodes = []string{"node-1", " "}
	assertInvalid(t, validateResource(&emptyNode, 16))

	dupNode := *ok
	dupNode.Nodes = []string{"node-1", "node-1"}
	assertInvalid(t, validateResource(&dupNode, 16))

	badGroup := *ok
	badGroup.Nodes = nil
	badGroup.NodeGroups = []string{"Bad_Group"}
	assertInvalid(t, validateResource(&badGroup, 16))

	dupGroup := *ok
	dupGroup.Nodes = nil
	dupGroup.NodeGroups = []string{"eda", "eda"}
	assertInvalid(t, validateResource(&dupGroup, 16))

	zeroPort := *ok
	zeroPort.Protocol = contract.SSHProtocol
	zeroPort.Nodes = []string{"node-1"}
	zeroPort.Port = uintPtr(0)
	assertInvalid(t, validateResource(&zeroPort, 16))

	bigPort := *ok
	bigPort.Protocol = contract.SSHProtocol
	bigPort.Nodes = []string{"node-1"}
	bigPort.Port = uintPtr(70000)
	assertInvalid(t, validateResource(&bigPort, 16))

	vncPort := *ok
	vncPort.Port = uintPtr(5901)
	assertInvalid(t, validateResource(&vncPort, 16))

	validSSH := *ok
	validSSH.Protocol = contract.SSHProtocol
	validSSH.Nodes = []string{"node-1"}
	validSSH.Port = uintPtr(2222)
	if err := validateResource(&validSSH, 16); err != nil {
		t.Fatalf("valid ssh port rejected: %v", err)
	}

	zeroDisplay := *ok
	z := 0
	zeroDisplay.Display = &z
	assertInvalid(t, validateResource(&zeroDisplay, 16))

	tooLargeDisplay := *ok
	huge := dynamicDisplayMax + 1
	tooLargeDisplay.Display = &huge
	assertInvalid(t, validateResource(&tooLargeDisplay, 16))

	fixedOK := *ok
	d99 := dynamicDisplayMax
	fixedOK.Display = &d99
	if err := validateResource(&fixedOK, 16); err != nil {
		t.Fatalf("display %d should be valid: %v", d99, err)
	}
}

func assertInvalid(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected invalid resource error")
	}
	domain, ok := errors.AsType[contract.DomainError](err)
	if !ok || domain.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("error = %v, want HTTP 400", err)
	}
}
