package diag

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/MarcoColomb0/rightsizer/internal/ontap"
)

func read(t *testing.T, b *Bundle) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	if err := b.WriteZip(&buf); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range z.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(data)
	}
	return out
}

func TestAnonymise(t *testing.T) {
	b := New()
	b.Anonymise = true
	b.Secrets = []string{"hunter2-password"}
	b.JSON("api.json", []any{
		map[string]any{"Path": "/api/storage/volumes?fields=name", "Body": map[string]any{"records": []any{
			map[string]any{"name": "data", "svm": map[string]any{"name": "svm_prod"}, "nas": map[string]any{"path": "/vol/data"}}}}},
		map[string]any{"Path": "/api/storage/luns", "Body": map[string]any{"records": []any{
			map[string]any{"name": "/vol/data/lun0", "serial_number": "wOj4Q]FMHlq7"}}}},
		map[string]any{"Path": "/api/network/ip/interfaces", "Body": map[string]any{"records": []any{
			map[string]any{"ip": map[string]any{"address": "10.1.2.3"}, "services": []any{"data_nfs"}}}}},
	})
	b.JSON("matching.json", []any{map[string]any{"VCenter": "vc01.corp", "Datastore": "Datastore 1", "Backing": "naa.6000c2 VMware"}})
	b.JSON("inventory.json", map[string]any{"Volumes": []any{map[string]any{"Name": "data", "SVM": "svm_prod"}}})
	b.Text("checks.txt", "[PASS] nodes: 2 nodes\n[PASS] volumes: 1 volumes\nvc01.corp / Datastore 1: not a NetApp ONTAP LUN (vendor \"VMware\")\n")
	b.Text("matching.txt", "ds-01 VMFS "+ontap.NAA("wOj4Q]FMHlq7")+" → svm_prod/data\nnfs-01 NFS 10.1.2.3:/vol/data → svm_prod/data\nlogin with hunter2-password\n")
	files := read(t, b)
	all := strings.Join([]string{files["inventory.json"], files["api.json"], files["matching.txt"], files["checks.txt"], files["matching.json"]}, "\n")
	for _, leak := range []string{"svm_prod", "wOj4Q", "10.1.2.3", "hunter2", "\"data\"", "/vol/data", "600a0980", "vc01.corp", "Datastore 1"} {
		if strings.Contains(all, leak) {
			t.Fatalf("%q left in the bundle:\n%s", leak, all)
		}
	}
	for _, kept := range []string{"data_nfs", "\"Volumes\"", "\"records\"", "fields=", "VMFS", "→", "/api/storage/volumes", "[PASS] nodes: 2 nodes", "[PASS] volumes", "VMware", "naa.6000c2"} {
		if !strings.Contains(all, kept) {
			t.Fatalf("%q must stay readable:\n%s", kept, all)
		}
	}
	// Relations survive: the volume keeps one token everywhere, and the
	// LUN's NAA identifier is anonymised like its serial number.
	m := files["matching.txt"]
	vol := strings.Fields(m)[4][strings.Index(strings.Fields(m)[4], "/")+1:]
	if !strings.HasPrefix(vol, "name-") || strings.Count(m, vol) != 3 || !strings.Contains(files["inventory.json"], vol) || !strings.Contains(m, "naa-") {
		t.Fatalf("tokens (volume %q):\n%s", vol, m)
	}
}

func TestAnonymiseConfiguredSources(t *testing.T) {
	b := New()
	b.Anonymise = true
	b.Identify("host", "cluster1-mgmt.corp.example.com:443", "vcsa01.corp.example.com")
	b.Identify("host", "10.20.30.40")
	b.Identify("user", "svc_monitor", "readonly@vsphere.local")
	b.JSON("collection.json", map[string]any{"Config": map[string]any{"Host": "cluster1-mgmt.corp.example.com:443", "User": "svc_monitor"}})
	b.Text("logs.txt", `level=INFO msg="source added" host=cluster1-mgmt.corp.example.com:443 kind=ONTAP
level=INFO msg="source added" host=vcsa01.corp.example.com user=readonly@vsphere.local
dial tcp 10.20.30.40:443: i/o timeout (cluster1-mgmt)
`)
	files := read(t, b)
	all := files["collection.json"] + files["logs.txt"]
	for _, leak := range []string{"cluster1-mgmt", "corp.example.com", "vcsa01", "10.20.30.40", "svc_monitor", "readonly"} {
		if strings.Contains(all, leak) {
			t.Fatalf("%q left in the bundle:\n%s", leak, all)
		}
	}
	for _, kept := range []string{":443", "kind=ONTAP", "i/o timeout", "source added"} {
		if !strings.Contains(all, kept) {
			t.Fatalf("%q must stay readable:\n%s", kept, all)
		}
	}
}

func TestPlainKeepsNamesButNotSecrets(t *testing.T) {
	b := New()
	b.Secrets = []string{"hunter2-password"}
	b.Text("log.txt", "svm_prod login hunter2-password")
	if got := read(t, b)["log.txt"]; got != "svm_prod login [secret]" {
		t.Fatalf("got %q", got)
	}
}
