package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/MarcoColomb0/rightsizer/internal/diag"
	"github.com/MarcoColomb0/rightsizer/internal/ontap"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
	rsversion "github.com/MarcoColomb0/rightsizer/internal/version"
)

const diagnoseUsage = `usage: rightsizer diagnose-ontap [-fingerprint FP] [-anonymise] [-o FILE] HOST USER

Reads everything rightsizer uses from an ONTAP cluster, with GET requests
only, checks it and writes a diagnostic bundle (zip). The password is read
from RIGHTSIZER_PASSWORD or asked for. A self-signed certificate must be
confirmed with -fingerprint, as shown on the first run.

The bundle goes to FILE, to stdout when it is not a terminal, or to the
current directory. With Docker:

  docker run --rm -i -e RIGHTSIZER_PASSWORD ghcr.io/marcocolomb0/rightsizer:TAG \
    diagnose-ontap -fingerprint FP cluster.example.local monitor > diag.zip
`

func diagnoseONTAP(args []string) error {
	if !rsversion.Diagnostics(version) {
		return errors.New("diagnose-ontap is available in pre-releases only")
	}
	fs := flag.NewFlagSet("diagnose-ontap", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, diagnoseUsage) }
	fp := fs.String("fingerprint", "", "SHA-256 fingerprint of the cluster's self-signed certificate")
	anon := fs.Bool("anonymise", false, "replace names, addresses, serial numbers and paths with tokens")
	out := fs.String("o", "", "write the bundle to this file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return errors.New("HOST and USER are required")
	}
	host, user := fs.Arg(0), fs.Arg(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	ci, err := vc.Probe(ctx, host)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", host, err)
	}
	pin := ""
	switch {
	case *fp != "":
		if !strings.EqualFold(strings.TrimSpace(*fp), ci.Fingerprint) {
			return fmt.Errorf("the certificate of %s has fingerprint %s, not the one given", host, ci.Fingerprint)
		}
		pin = ci.Fingerprint
	case !ci.Trusted:
		fmt.Fprintf(os.Stderr, "The certificate of %s is not signed by a trusted CA.\n  Subject  %s\n  Issuer   %s\n  SHA-256  %s\n\n"+
			"Compare it with: security certificate show -type server\nthen run again with -fingerprint %s\n", host, ci.Subject, ci.Issuer, ci.Fingerprint, ci.Fingerprint)
		return errors.New("certificate not confirmed")
	}

	password := os.Getenv("RIGHTSIZER_PASSWORD")
	if password == "" {
		if !term.IsTerminal(os.Stdin.Fd()) {
			return errors.New("set RIGHTSIZER_PASSWORD or run in a terminal")
		}
		fmt.Fprintf(os.Stderr, "Password for %s@%s: ", user, host)
		b, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		password = string(b)
	}

	cl, err := ontap.Connect(ctx, ontap.Credentials{Host: host, User: user, Password: password, Fingerprint: pin})
	if err != nil {
		return err
	}
	defer cl.Close(ctx)
	cl.Record()
	fmt.Fprintf(os.Stderr, "Connected: %s. Reading the cluster…\n", cl.About())
	d := ontap.Diagnose(ctx, cl)

	b := diag.New()
	b.Anonymise = *anon
	b.Secrets = []string{password}
	b.Identify("host", host)
	b.Identify("user", user)
	b.Text("README.txt", "rightsizer ONTAP diagnostic bundle, written by rightsizer diagnose-ontap.\n\n"+
		"checks.txt      what was checked, failures first\n"+
		"diagnosis.json  the cluster's records and archive ranges as rightsizer read them\n"+
		"api.json        every ONTAP REST request and response (GET only)\n"+
		"about.json      versions\n\n"+
		"Datastore matching needs the vCenters: build a bundle from the console (d on the cluster) to include it.\n")
	b.Text("checks.txt", d.Summary())
	b.JSON("diagnosis.json", d)
	b.JSON("api.json", cl.Recorded())
	b.JSON("about.json", map[string]any{"rightsizer": version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"generated": time.Now(), "anonymised": *anon, "tool": "diagnose-ontap"})

	fmt.Fprint(os.Stderr, "\n"+d.Summary()+"\n")
	switch {
	case *out != "":
	case !term.IsTerminal(os.Stdout.Fd()):
		return b.WriteZip(os.Stdout)
	default:
		*out = fmt.Sprintf("rightsizer-diag-%s.zip", time.Now().Format("20060102-150405"))
	}
	if err := b.Write(*out); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Bundle written to %s\n", *out)
	return nil
}
