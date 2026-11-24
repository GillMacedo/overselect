package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// obj is one Kubernetes object read from a manifest file.
type obj struct {
	file             string
	line             int
	apiVersion, kind string
	name, ns         string
	node             *Node
}

func (o *obj) ref() string { return o.kind + " " + o.name }

func (o *obj) where() string { return fmt.Sprintf("%s:%d", o.file, o.line) }

// get walks mapping keys from n and returns the node, or nil.
func get(n *Node, path ...string) *Node {
	for _, k := range path {
		if n == nil || n.Kind != MappingNode {
			return nil
		}
		var next *Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == k {
				next = n.Content[i+1]
			}
		}
		n = next
	}
	return n
}

func str(n *Node, path ...string) string {
	n = get(n, path...)
	if n == nil || n.Kind != ScalarNode {
		return ""
	}
	return n.Value
}

func items(n *Node) []*Node {
	if n == nil || n.Kind != SequenceNode {
		return nil
	}
	return n.Content
}

// stringMap reads a mapping of scalars, such as labels. ok is false when
// a value isn't a scalar, which Kubernetes would reject.
func stringMap(n *Node) (m map[string]string, ok bool) {
	m = map[string]string{}
	if n == nil {
		return m, true
	}
	if n.Kind != MappingNode {
		return m, n.Kind == ScalarNode && n.Tag == "!!null"
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		v := n.Content[i+1]
		if v.Kind != ScalarNode {
			return m, false
		}
		if v.Tag == "!!null" {
			m[n.Content[i].Value] = ""
			continue
		}
		m[n.Content[i].Value] = v.Value
	}
	return m, true
}

// manifestFiles expands arguments: files, directories (their .yaml, .yml
// and .json files, recursively) and globs.
func manifestFiles(args []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, a := range args {
		matches := []string{a}
		if strings.ContainsAny(a, "*?[") {
			m, err := filepath.Glob(a)
			if err != nil {
				return nil, usageErr("bad pattern %q: %v", a, err)
			}
			if len(m) == 0 {
				return nil, untrusted("pattern %q matches no file", a)
			}
			matches = m
		}
		for _, p := range matches {
			if p == "-" {
				add(p)
				continue
			}
			st, err := os.Stat(p)
			if err != nil {
				return nil, untrusted("manifest %s: %v", p, cleanErr(err))
			}
			if !st.IsDir() {
				add(p)
				continue
			}
			n := len(out)
			err = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if path != p && strings.HasPrefix(d.Name(), ".") {
						return filepath.SkipDir
					}
					return nil
				}
				switch strings.ToLower(filepath.Ext(path)) {
				case ".yaml", ".yml", ".json":
					add(path)
				}
				return nil
			})
			if err != nil {
				return nil, untrusted("reading %s: %v", p, cleanErr(err))
			}
			if len(out) == n {
				return nil, untrusted("directory %s has no .yaml, .yml or .json files", p)
			}
		}
	}
	return out, nil
}

// loadManifests reads every object in the files and refuses anything that
// isn't a complete set of Kubernetes objects.
func loadManifests(files []string, stdin io.Reader) ([]*obj, error) {
	var objs []*obj
	for _, f := range files {
		var src []byte
		var err error
		if f == "-" {
			src, err = io.ReadAll(stdin)
			f = "stdin"
		} else {
			src, err = os.ReadFile(f)
		}
		if err != nil {
			return nil, untrusted("manifest %s: %v", f, cleanErr(err))
		}
		if err := checkText(f, src); err != nil {
			return nil, err
		}
		if why := failedCommand(src); why != "" {
			return nil, untrusted("manifest %s looks like the output of a failed command (%s), not Kubernetes objects", f, why)
		}
		dec := NewDecoder(bytes.NewReader(src))
		n := 0
		for doc := 1; ; doc++ {
			var root Node
			err := dec.Decode(&root)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, untrusted("manifest %s isn't valid YAML, so it may be cut off: %s", f, firstLine(err.Error()))
			}
			body := &root
			if body.Kind == DocumentNode && len(body.Content) == 1 {
				body = body.Content[0]
			}
			if body.Kind == ScalarNode && body.Tag == "!!null" {
				continue // an empty document between --- lines
			}
			if body.Kind == MappingNode && buildConfig(str(body, "apiVersion")) {
				n++ // kustomize or skaffold configuration, not a cluster object
				continue
			}
			got, err := objectsIn(f, doc, body)
			if err != nil {
				return nil, err
			}
			objs = append(objs, got...)
			n += len(got)
		}
		if n == 0 {
			return nil, untrusted("manifest %s has no Kubernetes objects", f)
		}
	}
	return objs, nil
}

func objectsIn(file string, doc int, body *Node) ([]*obj, error) {
	if body.Kind != MappingNode {
		return nil, untrusted("manifest %s, document %d (line %d), isn't a Kubernetes object", file, doc, body.Line)
	}
	kind, api := str(body, "kind"), str(body, "apiVersion")
	if kind == "" || api == "" {
		return nil, untrusted("manifest %s, document %d (line %d), has no kind or apiVersion, so it isn't a Kubernetes object or it is cut off", file, doc, body.Line)
	}
	if buildConfig(api) {
		return nil, nil
	}
	if strings.HasSuffix(kind, "List") && get(body, "items") != nil {
		var out []*obj
		for _, it := range items(get(body, "items")) {
			got, err := objectsIn(file, doc, it)
			if err != nil {
				return nil, err
			}
			out = append(out, got...)
		}
		return out, nil
	}
	o := &obj{file: file, line: body.Line, apiVersion: api, kind: kind, node: body,
		name: str(body, "metadata", "name"), ns: str(body, "metadata", "namespace")}
	if o.name == "" && str(body, "metadata", "generateName") == "" {
		return nil, untrusted("manifest %s:%d: %s has no metadata.name, so it may be cut off", file, body.Line, kind)
	}
	if o.name == "" {
		o.name = str(body, "metadata", "generateName") + "*"
	}
	return []*obj{o}, nil
}

// buildConfig reports whether apiVersion belongs to a build tool's own
// configuration file, which sits next to manifests but isn't applied.
func buildConfig(api string) bool {
	return strings.HasPrefix(api, "kustomize.config.k8s.io/") || strings.HasPrefix(api, "skaffold/")
}

var failedOutputs = []string{
	"Error: ", "error: ", "Error from server", "panic: ", "fatal error: ", "FAILED", "make: ***", "unable to recognize",
	"The connection to the server", "no matches for kind", "command not found", "Traceback (most recent call last)",
	"walk.go:", "WARNING: ", "level=error", "Usage:",
}

// failedCommand recognizes the first line of common error output, from
// helm, kubectl, kustomize, make or a shell.
func failedCommand(src []byte) string {
	for _, line := range strings.Split(string(src), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || t == "---" {
			continue
		}
		for _, p := range failedOutputs {
			if strings.HasPrefix(t, p) {
				return "it starts with " + strconv.Quote(p)
			}
		}
		return ""
	}
	return ""
}

// checkText refuses files that can't be manifests at all.
func checkText(path string, src []byte) error {
	if len(bytes.TrimSpace(src)) == 0 {
		return untrusted("manifest %s is empty", path)
	}
	if bytes.IndexByte(src, 0) >= 0 {
		return untrusted("manifest %s is binary", path)
	}
	if !utf8.Valid(src) {
		return untrusted("manifest %s isn't valid UTF-8", path)
	}
	if src[len(src)-1] != '\n' {
		return untrusted("manifest %s doesn't end with a newline, so it may be cut off; if the file is complete, add a newline at the end", path)
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
