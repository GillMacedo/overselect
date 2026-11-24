package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// selector is a Kubernetes label selector: matchLabels plus
// matchExpressions. A Service's selector has matchLabels only.
type selector struct {
	labels map[string]string
	exprs  []expr
}

type expr struct {
	key, op string
	values  []string
}

// matches reports whether a pod with these labels is selected. An empty
// selector matches every pod, as it does for PodDisruptionBudgets and
// workloads.
func (s *selector) matches(labels map[string]string) bool {
	for k, v := range s.labels {
		got, ok := labels[k]
		if !ok || got != v {
			return false
		}
	}
	for _, e := range s.exprs {
		got, ok := labels[e.key]
		in := false
		for _, v := range e.values {
			if ok && got == v {
				in = true
			}
		}
		switch e.op {
		case "In":
			if !in {
				return false
			}
		case "NotIn":
			if in {
				return false
			}
		case "Exists":
			if !ok {
				return false
			}
		case "DoesNotExist":
			if ok {
				return false
			}
		}
	}
	return true
}

func (s *selector) empty() bool { return len(s.labels) == 0 && len(s.exprs) == 0 }

func (s *selector) String() string {
	var parts []string
	for _, k := range sortedKeys(s.labels) {
		parts = append(parts, k+"="+s.labels[k])
	}
	for _, e := range s.exprs {
		switch e.op {
		case "Exists":
			parts = append(parts, e.key)
		case "DoesNotExist":
			parts = append(parts, "!"+e.key)
		default:
			parts = append(parts, fmt.Sprintf("%s %s (%s)", e.key, strings.ToLower(e.op), strings.Join(e.values, ",")))
		}
	}
	if len(parts) == 0 {
		return "{} (every pod in the namespace)"
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// parseSelector reads a LabelSelector (matchLabels and matchExpressions).
func parseSelector(o *obj, n *Node) (*selector, error) {
	bad := func(what string) error {
		return untrusted("%s at %s has %s, which Kubernetes rejects, so the file may be cut off or broken", o.ref(), o.where(), what)
	}
	if n.Kind != MappingNode {
		if n.Kind == ScalarNode && n.Tag == "!!null" {
			return &selector{labels: map[string]string{}}, nil
		}
		return nil, bad("a selector that isn't a mapping")
	}
	ml, ok := stringMap(get(n, "matchLabels"))
	if !ok {
		return nil, bad("a matchLabels value that isn't a string")
	}
	s := &selector{labels: ml}
	for _, e := range items(get(n, "matchExpressions")) {
		x := expr{key: str(e, "key"), op: str(e, "operator")}
		for _, v := range items(get(e, "values")) {
			x.values = append(x.values, v.Value)
		}
		if x.key == "" {
			return nil, bad("a matchExpressions entry with no key")
		}
		switch x.op {
		case "In", "NotIn":
			if len(x.values) == 0 {
				return nil, bad("an " + x.op + " expression with no values")
			}
		case "Exists", "DoesNotExist":
			if len(x.values) > 0 {
				return nil, bad("an " + x.op + " expression with values")
			}
		default:
			return nil, bad(fmt.Sprintf("an unknown operator %q", x.op))
		}
		s.exprs = append(s.exprs, x)
	}
	if me := get(n, "matchExpressions"); me != nil && me.Kind != SequenceNode && !(me.Kind == ScalarNode && me.Tag == "!!null") {
		return nil, bad("matchExpressions that isn't a list")
	}
	return s, nil
}

// port is a port a pod template's containers declare.
type port struct {
	name   string
	number int
	image  string
}

// workload is anything with a pod template: the pods a selector can pick.
type workload struct {
	o      *obj
	labels map[string]string
	sel    *selector // nil for Jobs, CronJobs and bare Pods
	images []string  // image repositories, without tag or digest
	ports  []port
	job    bool // Job or CronJob: pods run to completion
}

func (w *workload) ref() string { return w.o.ref() }

// primary is the image repository of the first container, which is the
// main one by convention.
func (w *workload) primary() string {
	if len(w.images) == 0 {
		return ""
	}
	return w.images[0]
}

var selectorKinds = map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true, "ReplicaSet": true, "ReplicationController": true, "Rollout": true}

// templatePath is where each kind keeps its pod template.
func templatePath(kind string) []string {
	switch kind {
	case "Pod":
		return nil
	case "CronJob":
		return []string{"spec", "jobTemplate", "spec", "template"}
	}
	return []string{"spec", "template"}
}

func isWorkloadKind(kind string) bool {
	return selectorKinds[kind] || kind == "Job" || kind == "CronJob" || kind == "Pod"
}

// ownedByController reports whether a Pod or ReplicaSet belongs to another
// object, as in kubectl get output, so its owner already stands for it.
func ownedByController(o *obj) bool {
	for _, r := range items(get(o.node, "metadata", "ownerReferences")) {
		if str(r, "controller") == "true" {
			return true
		}
	}
	return false
}

// newWorkload reads the pod template and refuses one that is cut off or
// that Kubernetes would reject.
func newWorkload(o *obj) (*workload, error) {
	cut := func(what string) error {
		return untrusted("%s at %s has no %s, which Kubernetes requires, so the file may be cut off", o.ref(), o.where(), what)
	}
	bad := func(what string) error {
		return untrusted("%s at %s has %s, which Kubernetes rejects, so the file may be cut off or broken", o.ref(), o.where(), what)
	}
	tpl := o.node
	if p := templatePath(o.kind); p != nil {
		tpl = get(o.node, p...)
		if tpl == nil {
			return nil, cut(strings.Join(p, ".") + " (pod template)")
		}
	}
	spec := get(tpl, "spec")
	if spec == nil {
		return nil, cut("pod spec")
	}
	labels, ok := stringMap(get(tpl, "metadata", "labels"))
	if !ok {
		return nil, bad("a pod label value that isn't a string")
	}
	w := &workload{o: o, labels: labels, job: o.kind == "Job" || o.kind == "CronJob"}
	cs := items(get(spec, "containers"))
	if len(cs) == 0 {
		return nil, cut("containers")
	}
	all := append([]*Node{}, cs...)
	for _, c := range items(get(spec, "initContainers")) {
		if str(c, "restartPolicy") == "Always" {
			all = append(all, c) // a sidecar, which can serve a port
		}
	}
	seen := map[string]bool{}
	for _, c := range all {
		if str(c, "name") == "" {
			return nil, cut("name on a container")
		}
		img := str(c, "image")
		if img == "" {
			return nil, cut("image on container " + str(c, "name"))
		}
		repo := imageRepo(img)
		if repo != "" && !seen[repo] {
			seen[repo] = true
			w.images = append(w.images, repo)
		}
		for _, p := range items(get(c, "ports")) {
			n, err := strconv.Atoi(str(p, "containerPort"))
			if err != nil || n <= 0 || n > 65535 {
				return nil, bad("a container port that isn't a number from 1 to 65535")
			}
			w.ports = append(w.ports, port{name: str(p, "name"), number: n, image: repo})
		}
	}
	if !selectorKinds[o.kind] {
		return w, nil
	}
	sn := get(o.node, "spec", "selector")
	if sn == nil {
		if o.kind == "ReplicationController" {
			// An RC's selector defaults to its template labels.
			w.sel = &selector{labels: labels}
			return w, nil
		}
		return nil, cut("spec.selector")
	}
	var err error
	if o.kind == "ReplicationController" {
		m, ok := stringMap(sn)
		if !ok {
			return nil, bad("a selector value that isn't a string")
		}
		w.sel = &selector{labels: m}
	} else if w.sel, err = parseSelector(o, sn); err != nil {
		return nil, err
	}
	if w.sel.empty() {
		return nil, bad("an empty spec.selector")
	}
	if !w.sel.matches(labels) {
		return nil, untrusted("%s at %s has a spec.selector (%s) that doesn't match its own pod template labels, which Kubernetes rejects, so the file may be cut off or broken", o.ref(), o.where(), w.sel)
	}
	return w, nil
}

// imageRepo drops the tag and digest, and Docker Hub's default prefixes,
// so the same image at two versions compares equal.
func imageRepo(img string) string {
	if i := strings.Index(img, "@"); i >= 0 {
		img = img[:i]
	}
	if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") {
		img = img[:i]
	}
	img = strings.TrimPrefix(img, "docker.io/")
	img = strings.TrimPrefix(img, "index.docker.io/")
	img = strings.TrimPrefix(img, "library/")
	return img
}

// service is a Service with a selector.
type service struct {
	o     *obj
	sel   *selector
	ports []svcPort
}

type svcPort struct {
	name      string
	port      int
	target    int    // numeric targetPort, or 0
	targetStr string // named targetPort, or ""
	dnsOnly   bool   // a headless Service with no ports
}

func (p svcPort) String() string {
	if p.dnsOnly {
		return "DNS records (headless, no ports)"
	}
	t := strconv.Itoa(p.target)
	if p.targetStr != "" {
		t = p.targetStr
	}
	if t == strconv.Itoa(p.port) {
		return "port " + t
	}
	return fmt.Sprintf("port %d -> %s", p.port, t)
}

// newService returns nil for a Service without a selector, whose endpoints
// are managed by hand or by a controller.
func newService(o *obj) (*service, error) {
	cut := func(what string) error {
		return untrusted("%s at %s has no %s, which Kubernetes requires, so the file may be cut off", o.ref(), o.where(), what)
	}
	bad := func(what string) error {
		return untrusted("%s at %s has %s, which Kubernetes rejects, so the file may be cut off or broken", o.ref(), o.where(), what)
	}
	if str(o.node, "spec", "type") == "ExternalName" {
		return nil, nil
	}
	if get(o.node, "spec") == nil {
		return nil, cut("spec")
	}
	ps := items(get(o.node, "spec", "ports"))
	headless := str(o.node, "spec", "clusterIP") == "None"
	if len(ps) == 0 && !headless {
		return nil, cut("spec.ports")
	}
	m, ok := stringMap(get(o.node, "spec", "selector"))
	if !ok {
		return nil, bad("a selector value that isn't a string")
	}
	s := &service{o: o, sel: &selector{labels: m}}
	for _, p := range ps {
		n, err := strconv.Atoi(str(p, "port"))
		if err != nil || n <= 0 || n > 65535 {
			return nil, bad("a port that isn't a number from 1 to 65535")
		}
		sp := svcPort{name: str(p, "name"), port: n, target: n}
		if t := get(p, "targetPort"); t != nil {
			if v, err := strconv.Atoi(t.Value); err == nil {
				sp.target = v
			} else if t.Kind == ScalarNode && t.Value != "" {
				sp.target, sp.targetStr = 0, t.Value
			} else {
				return nil, bad("a targetPort that isn't a number or a name")
			}
		}
		s.ports = append(s.ports, sp)
	}
	if len(ps) == 0 {
		s.ports = []svcPort{{dnsOnly: true}} // headless: DNS records for every selected pod
	}
	if len(m) == 0 {
		return nil, nil // no selector: Kubernetes manages no endpoints for it
	}
	return s, nil
}

// pdb is a PodDisruptionBudget.
type pdb struct {
	o      *obj
	sel    *selector
	policy string // "minAvailable: 1", "maxUnavailable: 25%"
	byOne  bool   // the policy only works for pods of one controller
}

func newPDB(o *obj) (*pdb, error) {
	sn := get(o.node, "spec", "selector")
	if sn == nil {
		return nil, untrusted("%s at %s has no spec.selector, so it covers no pods; the file may be cut off", o.ref(), o.where())
	}
	sel, err := parseSelector(o, sn)
	if err != nil {
		return nil, err
	}
	p := &pdb{o: o, sel: sel}
	if v := str(o.node, "spec", "maxUnavailable"); v != "" {
		p.policy, p.byOne = "maxUnavailable: "+v, true
	} else if v := str(o.node, "spec", "minAvailable"); v != "" {
		p.policy, p.byOne = "minAvailable: "+v, strings.HasSuffix(v, "%")
	}
	return p, nil
}

// hpa is a HorizontalPodAutoscaler's target.
type hpa struct {
	o            *obj
	kind, target string
}

// cluster holds everything read from the manifests.
type cluster struct {
	workloads []*workload
	services  []*service
	pdbs      []*pdb
	hpas      []*hpa
}

func newCluster(objs []*obj) (*cluster, error) {
	c := &cluster{}
	for _, o := range objs {
		switch {
		case helmTest(o):
			continue // exists only while helm test runs
		case isWorkloadKind(o.kind):
			if (o.kind == "Pod" || o.kind == "ReplicaSet" || o.kind == "Job") && ownedByController(o) {
				continue // its owner is in the list too, or it's managed elsewhere
			}
			w, err := newWorkload(o)
			if err != nil {
				return nil, err
			}
			c.workloads = append(c.workloads, w)
		case o.kind == "Service" && o.apiVersion == "v1":
			s, err := newService(o)
			if err != nil {
				return nil, err
			}
			if s != nil {
				c.services = append(c.services, s)
			}
		case o.kind == "PodDisruptionBudget":
			p, err := newPDB(o)
			if err != nil {
				return nil, err
			}
			c.pdbs = append(c.pdbs, p)
		case o.kind == "HorizontalPodAutoscaler":
			k, n := str(o.node, "spec", "scaleTargetRef", "kind"), str(o.node, "spec", "scaleTargetRef", "name")
			if k == "" || n == "" {
				return nil, untrusted("%s at %s has no spec.scaleTargetRef kind and name, which Kubernetes requires, so the file may be cut off", o.ref(), o.where())
			}
			if str(o.node, "spec", "maxReplicas") == "" {
				return nil, untrusted("%s at %s has no spec.maxReplicas, which Kubernetes requires, so the file may be cut off", o.ref(), o.where())
			}
			c.hpas = append(c.hpas, &hpa{o: o, kind: k, target: n})
		}
	}
	return c, nil
}

// sameNS treats a missing namespace as the one the files are applied to,
// which can be any of the named ones.
func sameNS(a, b string) bool { return a == "" || b == "" || a == b }

// selected returns the workloads whose pod templates match sel.
func (c *cluster) selected(ns string, sel *selector, skip *workload) []*workload {
	var out []*workload
	for _, w := range c.workloads {
		if w != skip && sameNS(ns, w.o.ns) && sel.matches(w.labels) {
			out = append(out, w)
		}
	}
	return out
}

// helmTest reports whether o is a Helm test hook, which Helm creates only
// while helm test runs.
func helmTest(o *obj) bool {
	m, _ := stringMap(get(o.node, "metadata", "annotations"))
	for _, h := range strings.Split(m["helm.sh/hook"], ",") {
		if h := strings.TrimSpace(h); h == "test" || h == "test-success" || h == "test-failure" {
			return true
		}
	}
	return false
}
