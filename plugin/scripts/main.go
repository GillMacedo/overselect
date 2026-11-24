// Command overselect reads Kubernetes manifests and reports label selectors
// that match the pods of more than one workload: Services that send
// traffic to the wrong pods, pods covered by two PodDisruptionBudgets, and
// workloads whose selectors overlap.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

const version = "0.1.0"

// Exit codes.
const (
	exitOK       = 0
	exitFindings = 1
	exitUsage    = 2
	exitInput    = 3
)

// inputError means the input can't be trusted, so no result is printed.
type inputError struct{ msg string }

func (e inputError) Error() string { return e.msg }

func untrusted(format string, a ...any) error { return inputError{fmt.Sprintf(format, a...)} }

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usageErr(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

// cleanErr drops the operation prefix from path errors, which already
// name the path in the message around them.
func cleanErr(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

type options struct {
	format string
	failOn string
	paths  []string
}

func parseArgs(args []string, stderr io.Writer) (*options, bool, error) {
	fl := flag.NewFlagSet("overselect", flag.ContinueOnError)
	fl.SetOutput(stderr)
	var o options
	showVersion := false
	fl.StringVar(&o.format, "format", "text", "output format: text or json")
	fl.StringVar(&o.failOn, "fail-on", "warn", "exit 1 on findings at this level or worse: error, warn or never")
	fl.BoolVar(&showVersion, "version", false, "print the version")
	fl.Usage = func() {
		fmt.Fprint(stderr, `usage: overselect [flags] MANIFEST...

Reports label selectors that match the pods of more than one workload:
Services, PodDisruptionBudgets and workload spec.selector. MANIFEST is a
file, a directory of .yaml, .yml and .json files, a glob, or - for stdin.
Render Helm charts first: helm template my-release ./chart | overselect -

Flags:
`)
		fl.PrintDefaults()
		fmt.Fprint(stderr, `
Exit codes: 0 nothing found, 1 findings at --fail-on level, 2 bad usage,
3 input that can't be trusted or nothing to compare.
`)
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, true, nil
		}
		return nil, false, usageError{err.Error()}
	}
	if showVersion {
		fmt.Fprintln(stderr, "overselect", version)
		return nil, true, nil
	}
	o.paths = fl.Args()
	switch o.format {
	case "text", "json":
	default:
		return nil, false, usageErr("--format must be text or json, not %q", o.format)
	}
	switch o.failOn {
	case "error", "warn", "never":
	default:
		return nil, false, usageErr("--fail-on must be error, warn or never, not %q", o.failOn)
	}
	if len(o.paths) == 0 {
		return nil, false, usageErr("name at least one manifest file or directory, or - for stdin")
	}
	return &o, false, nil
}

func run(o *options, stdin io.Reader) ([]finding, counts, error) {
	files, err := manifestFiles(o.paths)
	if err != nil {
		return nil, counts{}, err
	}
	objs, err := loadManifests(files, stdin)
	if err != nil {
		return nil, counts{}, err
	}
	c, err := newCluster(objs)
	if err != nil {
		return nil, counts{}, err
	}
	where := strings.Join(o.paths, " ")
	if len(c.workloads) == 0 {
		return nil, counts{}, untrusted("%s has no pod templates (Deployment, StatefulSet, DaemonSet, ReplicaSet, Job, CronJob or Pod), so there is nothing a selector could match", where)
	}
	fs, n := check(c)
	if n.total() == 0 {
		return nil, counts{}, untrusted("nothing to compare in %s: no Service or PodDisruptionBudget selects any of its %s, and no namespace has two workloads", where, plural(n.PodTemplates, "pod template", "pod templates"))
	}
	return fs, n, nil
}

func main() {
	os.Exit(mainCode(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func mainCode(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	o, done, err := parseArgs(args, stderr)
	if done {
		return exitOK
	}
	if err != nil {
		fmt.Fprintln(stderr, "overselect:", err)
		return exitUsage
	}
	fs, n, err := run(o, stdin)
	if err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintln(stderr, "overselect:", err)
			return exitUsage
		}
		fmt.Fprintf(stderr, "overselect: can't trust the input, so there is no result: %v\n", err)
		return exitInput
	}
	if o.format == "json" {
		writeJSON(stdout, fs, n)
	} else {
		writeText(stdout, fs, n)
	}
	if failing(fs, o.failOn) {
		return exitFindings
	}
	return exitOK
}

func failing(fs []finding, failOn string) bool {
	for _, f := range fs {
		switch {
		case failOn == "never":
		case f.Level == levelError:
			return true
		case f.Level == levelWarn && failOn == "warn":
			return true
		}
	}
	return false
}

func header(n counts) string {
	return fmt.Sprintf("overselect: %s, %s and %s checked against %s",
		plural(n.Services, "Service", "Services"), plural(n.PDBs, "PodDisruptionBudget", "PodDisruptionBudgets"),
		plural(n.Selectors, "workload selector", "workload selectors"), plural(n.PodTemplates, "pod template", "pod templates"))
}

func writeText(w io.Writer, fs []finding, n counts) {
	fmt.Fprintln(w, header(n))
	problems, canary := 0, false
	var unmatched []finding
	for _, f := range fs {
		if f.Code == "OS901" {
			unmatched = append(unmatched, f)
			continue
		}
		fmt.Fprintf(w, "\n%s %s %s:%d  %s\n", strings.ToUpper(f.Level), f.Code, f.File, f.Line, f.Summary)
		for _, d := range f.Details {
			fmt.Fprintf(w, "  %s\n", d)
		}
		if f.Fix != "" {
			fmt.Fprintf(w, "  Fix: %s\n", f.Fix)
		}
		if f.Level != levelNote {
			problems++
		}
		if f.Code == "OS103" {
			canary = true
		}
	}
	if len(unmatched) > 0 {
		fmt.Fprintf(w, "\nWARN OS901  %s no pod template in these files, so %s compared. The pods come from somewhere else, such as an operator or a file not passed in, or the files are incomplete:\n",
			plural(len(unmatched), "selector matches", "selectors match"), map[bool]string{true: "it wasn't", false: "they weren't"}[len(unmatched) == 1])
		for _, f := range unmatched {
			fmt.Fprintf(w, "  %s (%s:%d)\n", f.Object, f.File, f.Line)
		}
	}
	fmt.Fprintln(w)
	switch {
	case problems > 0:
		fmt.Fprintf(w, "Result: %s where a selector matches pods it probably shouldn't.\n", plural(problems, "finding", "findings"))
		if len(unmatched) > 0 {
			fmt.Fprintf(w, "%s matched nothing and %s checked.\n", plural(len(unmatched), "selector", "selectors"), map[bool]string{true: "wasn't", false: "weren't"}[len(unmatched) == 1])
		}
	case len(unmatched) > 0:
		fmt.Fprintf(w, "Result: no overlap among the selectors compared, but %s matched nothing in these files, so the result is incomplete. Pass the files that define those pods, or use --fail-on error if an operator creates them.\n", plural(len(unmatched), "selector", "selectors"))
	case canary:
		fmt.Fprintln(w, "Result: every selector checked matches the pods of one workload, or of workloads that run the same image.")
	default:
		fmt.Fprintln(w, "Result: every selector checked matches the pods of one workload.")
	}
	fmt.Fprintln(w, "overselect reads manifests only. Pods created by operators or defined in other files aren't seen.")
}

func writeJSON(w io.Writer, fs []finding, n counts) {
	if fs == nil {
		fs = []finding{}
	}
	out := struct {
		Version  string    `json:"version"`
		Checked  counts    `json:"checked"`
		Findings []finding `json:"findings"`
	}{version, n, fs}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(out)
}
