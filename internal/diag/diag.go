// Package diag writes diagnostic bundles: a zip of what rightsizer read and
// concluded, to share when something does not work as expected. Bundles are
// written locally and shared only by the operator; nothing is sent anywhere.
package diag

import (
	"archive/zip"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Bundle collects files in the order they are added.
type Bundle struct {
	names []string
	files map[string][]byte
	// Anonymise replaces names, addresses, serial numbers and paths with
	// stable tokens before writing.
	Anonymise bool
	// Secrets are left out whatever happens, such as passwords.
	Secrets []string
}

func New() *Bundle { return &Bundle{files: map[string][]byte{}} }

func (b *Bundle) put(name string, data []byte) {
	if _, ok := b.files[name]; !ok {
		b.names = append(b.names, name)
	}
	b.files[name] = data
}

// Text adds a text file.
func (b *Bundle) Text(name, s string) { b.put(name, []byte(s)) }

// JSON adds a value as indented JSON.
func (b *Bundle) JSON(name string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		data = []byte(`{"error": ` + strconvQuote(err.Error()) + `}`)
	}
	b.put(name, data)
}

func strconvQuote(s string) string {
	q, _ := json.Marshal(s)
	return string(q)
}

// Names lists the files in the bundle.
func (b *Bundle) Names() []string { return slices.Clone(b.names) }

// Write saves the bundle as a zip at path.
func (b *Bundle) Write(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := b.WriteZip(f); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// WriteZip writes the bundle as a zip.
func (b *Bundle) WriteZip(w io.Writer) error {
	var a *anonymiser
	if b.Anonymise {
		a = newAnonymiser()
		for _, n := range b.names {
			a.collect(n, b.files[n])
		}
	}
	z := zip.NewWriter(w)
	now := time.Now()
	for _, n := range b.names {
		data := b.files[n]
		for _, s := range b.Secrets {
			if len(s) >= 4 {
				data = []byte(strings.ReplaceAll(string(data), s, "[secret]"))
			}
		}
		if a != nil {
			data = a.apply(data)
		}
		fw, err := z.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: now})
		if err != nil {
			return err
		}
		if _, err := fw.Write(data); err != nil {
			return err
		}
	}
	return z.Close()
}

// ontapKeys are the fields of ONTAP records that identify the customer.
var ontapKeys = map[string]string{"name": "name", "serial_number": "serial", "address": "ip", "path": "path", "system_id": "serial", "ip": "ip"}

// datastoreKeys are the fields of matching rows taken from vCenter.
var datastoreKeys = map[string]string{"VCenter": "host", "Datastore": "name", "Volume": "path", "Backing": "export"}

var ipRE = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)

// anonymiser maps each sensitive value to a token derived with a key that
// is never stored, so the same value gets the same token within a bundle
// and cannot be recovered from it.
type anonymiser struct {
	key []byte
	m   map[string]string
}

func newAnonymiser() *anonymiser {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return &anonymiser{key: key, m: map[string]string{}}
}

func (a *anonymiser) token(kind, v string) string {
	if t, ok := a.m[v]; ok {
		return t
	}
	h := hmac.New(sha256.New, a.key)
	h.Write([]byte(v))
	t := kind + "-" + hex.EncodeToString(h.Sum(nil))[:8]
	a.m[v] = t
	return t
}

// collect records the sensitive values of a file: the records inside the
// ONTAP responses of api.json and the vCenter side of matching.json.
// Everything else is only replaced, so check names and field names stay
// readable.
func (a *anonymiser) collect(name string, data []byte) {
	switch name {
	case "api.json":
		var xs []struct{ Body any }
		if json.Unmarshal(data, &xs) == nil {
			for _, x := range xs {
				a.walk(ontapKeys, "", x.Body)
			}
		}
	case "matching.json":
		var v any
		if json.Unmarshal(data, &v) == nil {
			a.walk(datastoreKeys, "", v)
		}
	}
}

func (a *anonymiser) walk(keys map[string]string, key string, v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			a.walk(keys, k, vv)
		}
	case []any:
		for _, vv := range x {
			a.walk(keys, key, vv)
		}
	case string:
		kind, ok := keys[key]
		if !ok || len(x) < 2 {
			return
		}
		switch kind {
		case "serial":
			a.token("serial", x)
			// The NAA identifier hosts see derives from the serial.
			a.m["naa.600a0980"+hex.EncodeToString([]byte(x))] = "naa-" + strings.TrimPrefix(a.token("serial", x), "serial-")
		case "path", "export":
			if kind == "export" && !strings.Contains(x, ":/") {
				// LUN device identifiers and vendors say why a LUN did not
				// match and identify nobody.
				return
			}
			for _, part := range strings.FieldsFunc(x, func(r rune) bool { return r == '/' || r == ':' || r == ',' || r == ' ' }) {
				if len(part) > 1 && part != "vol" {
					a.token(tokenKind(part), part)
				}
			}
		default:
			a.token(kind, x)
		}
	}
}

func tokenKind(s string) string {
	if ipRE.MatchString(s) {
		return "ip"
	}
	return "name"
}

// apply replaces whole tokens (runs of letters, digits and _ . -) equal to
// a collected value, so a volume named "data" leaves data_nfs alone. Serial
// numbers can hold punctuation and are replaced wherever they appear.
func (a *anonymiser) apply(data []byte) []byte {
	// Serial numbers and names with spaces or punctuation are replaced as
	// substrings, longest first; the rest as whole tokens below.
	var exact []string
	for k := range a.m {
		if strings.HasPrefix(a.m[k], "serial-") || strings.IndexFunc(k, func(r rune) bool { return !isWord(r) }) >= 0 {
			exact = append(exact, k)
		}
	}
	slices.SortFunc(exact, func(x, y string) int { return len(y) - len(x) })
	pairs := make([]string, 0, 2*len(exact))
	for _, k := range exact {
		pairs = append(pairs, k, a.m[k])
	}
	s := strings.NewReplacer(pairs...).Replace(string(data))
	var b strings.Builder
	b.Grow(len(s))
	word := func(r byte) bool { return isWord(rune(r)) }
	for i := 0; i < len(s); {
		if !word(s[i]) {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && word(s[j]) {
			j++
		}
		run := s[i:j]
		if t, ok := a.m[run]; ok {
			b.WriteString(t)
		} else if t, ok := a.m[strings.TrimRight(run, ".")]; ok {
			b.WriteString(t + run[len(strings.TrimRight(run, ".")):])
		} else {
			b.WriteString(run)
		}
		i = j
	}
	return []byte(b.String())
}

func isWord(r rune) bool {
	return r == '_' || r == '.' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}
