package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deploy renders a Deployment with the given selector and pod labels.
func deploy(name, sel, labels, image, ports string) string {
	return `apiVersion: apps/v1
kind: Deployment
metadata:
  name: ` + name + `
spec:
  selector:
    matchLabels: {` + sel + `}
  template:
    metadata:
      labels: {` + labels + `}
    spec:
      containers:
        - name: c
          image: ` + image + `
          ports: [` + ports + `]
`
}

func svc(name, sel, target string) string {
	return `apiVersion: v1
kind: Service
metadata:
  name: ` + name + `
spec:
  selector: {` + sel + `}
  ports:
    - port: 80
      targetPort: ` + target + `
`
}

func pdbDoc(name, sel, policy string) string {
	return `apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: ` + name + `
spec:
  ` + policy + `
  selector:
    matchLabels: {` + sel + `}
`
}

// runDocs writes the documents to one file and runs overselect on it.
func runDocs(t *testing.T, args []string, docs ...string) (string, string, int) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "m.yaml")
	if err := os.WriteFile(p, []byte(strings.Join(docs, "---\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := mainCode(append(append([]string{}, args...), p), strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), code
}

func codes(out string) []string {
	var cs []string
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && (f[0] == "ERROR" || f[0] == "WARN" || f[0] == "NOTE") {
			cs = append(cs, f[1])
		}
	}
	return cs
}

func wantCodes(t *testing.T, out string, want ...string) {
	t.Helper()
	got := codes(out)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("codes %v, want %v\n%s", got, want, out)
	}
}

const (
	api    = "app: api, release: r"
	apiTpl = "app: api, release: r, component: server"
	sink   = "app: api, release: r, component: mail"
)

func TestSelectorMatches(t *testing.T) {
	l := map[string]string{"app": "api", "tier": "web"}
	cases := []struct {
		s    selector
		want bool
	}{
		{selector{labels: map[string]string{}}, true},
		{selector{labels: map[string]string{"app": "api"}}, true},
		{selector{labels: map[string]string{"app": "web"}}, false},
		{selector{labels: map[string]string{"app": "api", "x": "y"}}, false},
		{selector{exprs: []expr{{"tier", "In", []string{"web", "db"}}}}, true},
		{selector{exprs: []expr{{"tier", "In", []string{"db"}}}}, false},
		{selector{exprs: []expr{{"tier", "NotIn", []string{"db"}}}}, true},
		{selector{exprs: []expr{{"tier", "NotIn", []string{"web"}}}}, false},
		{selector{exprs: []expr{{"missing", "NotIn", []string{"web"}}}}, true},
		{selector{exprs: []expr{{"tier", "Exists", nil}}}, true},
		{selector{exprs: []expr{{"missing", "Exists", nil}}}, false},
		{selector{exprs: []expr{{"missing", "DoesNotExist", nil}}}, true},
		{selector{exprs: []expr{{"app", "DoesNotExist", nil}}}, false},
	}
	for i, c := range cases {
		if got := c.s.matches(l); got != c.want {
			t.Errorf("case %d (%s): got %v", i, c.s.String(), got)
		}
	}
}

func TestImageRepo(t *testing.T) {
	for in, want := range map[string]string{
		"nginx":                      "nginx",
		"nginx:1.27":                 "nginx",
		"docker.io/library/nginx:1":  "nginx",
		"ghcr.io/a/b:v1@sha256:abcd": "ghcr.io/a/b",
		"localhost:5000/app":         "localhost:5000/app",
		"localhost:5000/app:2":       "localhost:5000/app",
	} {
		if got := imageRepo(in); got != want {
			t.Errorf("imageRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServiceAcrossImages(t *testing.T) {
	out, _, code := runDocs(t, nil,
		svc("r-api", api, "8080"),
		deploy("r-api", api, apiTpl, "example/api:1", "{containerPort: 8080}"),
		deploy("r-mail", sink, sink, "axllent/mailpit:1", "{containerPort: 1025}"))
	wantCodes(t, out, "OS101", "OS301")
	if code != 1 || !strings.Contains(out, "Deployment r-mail (axllent/mailpit) doesn't declare 8080") || !strings.Contains(out, "add component=server to its selector") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestServiceNamedPortExcludesPodsWithout(t *testing.T) {
	// A named targetPort leaves pods without that name out of the
	// endpoints, so only one workload gets traffic.
	out, _, code := runDocs(t, nil,
		svc("r-api", api, "web"),
		deploy("r-api", api, apiTpl, "example/api:1", "{name: web, containerPort: 8080}"),
		deploy("r-mail", sink, sink, "axllent/mailpit:1", "{name: smtp, containerPort: 1025}"))
	wantCodes(t, out, "OS301")
	if code != 1 {
		t.Errorf("exit %d", code)
	}
	// The same port name on both sends traffic to the other one's port.
	out, _, _ = runDocs(t, nil,
		svc("r-api", api, "http"),
		deploy("r-api", api, apiTpl, "example/api:1", "{name: http, containerPort: 8080}"),
		deploy("r-mail", sink, sink, "axllent/mailpit:1", "{name: http, containerPort: 8025}"))
	wantCodes(t, out, "OS101", "OS301")
	if !strings.Contains(out, "Deployment r-mail (axllent/mailpit) serves it on 8025") {
		t.Error(out)
	}
}

func TestServiceNoEndpoints(t *testing.T) {
	out, _, code := runDocs(t, nil,
		svc("r-api", apiTpl, "metrics"),
		deploy("r-api", apiTpl, apiTpl, "example/api:1", "{name: http, containerPort: 8080}"))
	wantCodes(t, out, "OS104")
	if code != 1 {
		t.Errorf("exit %d", code)
	}
}

func TestCanaryIsNote(t *testing.T) {
	stable := "app: api, track: stable"
	canary := "app: api, track: canary"
	out, _, code := runDocs(t, nil,
		svc("api", "app: api", "8080"),
		deploy("api", stable, stable, "example/api:1.4", "{containerPort: 8080}"),
		deploy("api-canary", canary, canary, "example/api:1.5", "{containerPort: 8080}"))
	wantCodes(t, out, "OS103")
	if code != 0 || !strings.Contains(out, "or of workloads that run the same image") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestServiceSelectsJob(t *testing.T) {
	job := `apiVersion: batch/v1
kind: Job
metadata:
  name: r-migrate
spec:
  template:
    metadata:
      labels: {` + apiTpl + `, job: migrate}
    spec:
      restartPolicy: Never
      containers: [{name: m, image: example/api:1}]
`
	out, _, code := runDocs(t, nil,
		svc("r-api", apiTpl, "8080"),
		deploy("r-api", apiTpl, apiTpl, "example/api:1", "{containerPort: 8080}"),
		job)
	wantCodes(t, out, "OS102")
	if code != 1 {
		t.Errorf("exit %d", code)
	}
	if _, _, code := runDocs(t, []string{"--fail-on", "error"}, svc("r-api", apiTpl, "8080"), deploy("r-api", apiTpl, apiTpl, "example/api:1", "{containerPort: 8080}"), job); code != 0 {
		t.Errorf("--fail-on error with a warning: exit %d", code)
	}
}

func TestTwoBudgets(t *testing.T) {
	out, _, code := runDocs(t, nil,
		pdbDoc("a", "app: api", "minAvailable: 1"),
		pdbDoc("b", "component: server", "maxUnavailable: 1"),
		deploy("r-api", apiTpl, apiTpl, "example/api:1", ""))
	wantCodes(t, out, "OS201")
	if code != 1 || !strings.Contains(out, "more than one PodDisruptionBudget") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestBudgetAcrossWorkloads(t *testing.T) {
	out, _, _ := runDocs(t, nil,
		pdbDoc("r-api", api, "maxUnavailable: 25%"),
		deploy("r-api", api, apiTpl, "example/api:1", ""),
		deploy("r-mail", sink, sink, "axllent/mailpit:1", ""))
	wantCodes(t, out, "OS301", "OS202")
	if !strings.Contains(out, "maxUnavailable and percentage minAvailable only for pods that all have the same controller") {
		t.Error(out)
	}
}

func TestOverlappingSelectorsGrouped(t *testing.T) {
	w := "app: worker"
	out, _, code := runDocs(t, nil,
		deploy("w1", w, w, "example/w:1", ""),
		deploy("w2", w, w, "example/w:1", ""),
		deploy("w3", w, w, "example/w:1", ""))
	wantCodes(t, out, "OS301")
	if code != 1 || !strings.Contains(out, "3 workloads select each other's pods") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestOverlapWithHPA(t *testing.T) {
	h := `apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: r-api
spec:
  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: r-api}
  maxReplicas: 5
`
	out, _, _ := runDocs(t, nil, deploy("r-api", api, apiTpl, "example/api:1", ""), deploy("r-mail", sink, sink, "m:1", ""), h)
	if !strings.Contains(out, "HorizontalPodAutoscaler r-api scales Deployment r-api on the metrics of every pod its selector matches, so the pods of Deployment r-mail count too") {
		t.Error(out)
	}
}

func TestNamespacesKeepApart(t *testing.T) {
	a := strings.Replace(deploy("api", "app: api", "app: api", "a:1", ""), "  name: api\n", "  name: api\n  namespace: one\n", 1)
	b := strings.Replace(deploy("api", "app: api", "app: api", "a:1", ""), "  name: api\n", "  name: api\n  namespace: two\n", 1)
	out, _, code := runDocs(t, nil, a, b)
	_ = out
	// Two namespaces, one workload each: nothing to compare.
	if code != exitInput {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestHealthyStaysQuiet(t *testing.T) {
	worker := "app: api, release: r, component: worker"
	out, _, code := runDocs(t, nil,
		svc("r-api", apiTpl, "8080"),
		pdbDoc("r-api", apiTpl, "minAvailable: 1"),
		deploy("r-api", apiTpl, apiTpl, "example/api:1", "{containerPort: 8080}"),
		deploy("r-worker", worker, worker, "example/api:1", ""))
	wantCodes(t, out)
	if code != 0 || !strings.Contains(out, "Result: every selector checked matches the pods of one workload.") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestOwnedAndTestPodsSkipped(t *testing.T) {
	owned := `apiVersion: v1
kind: Pod
metadata:
  name: r-api-abc
  labels: {` + apiTpl + `}
  ownerReferences: [{apiVersion: apps/v1, kind: ReplicaSet, name: r-api-1, uid: x, controller: true}]
spec:
  containers: [{name: c, image: example/api:1}]
`
	test := `apiVersion: v1
kind: Pod
metadata:
  name: r-api-test
  labels: {` + apiTpl + `}
  annotations: {helm.sh/hook: test}
spec:
  containers: [{name: c, image: busybox}]
`
	out, _, code := runDocs(t, nil, svc("r-api", apiTpl, "8080"), deploy("r-api", apiTpl, apiTpl, "example/api:1", "{containerPort: 8080}"), owned, test)
	wantCodes(t, out)
	if code != 0 {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestBarePodAdoptedByReplicaSet(t *testing.T) {
	rs := strings.Replace(deploy("r-api", "app: api", "app: api", "example/api:1", ""), "kind: Deployment", "kind: ReplicaSet", 1)
	bare := "apiVersion: v1\nkind: Pod\nmetadata:\n  name: debug\n  labels: {app: api}\nspec:\n  containers: [{name: c, image: busybox}]\n"
	out, _, _ := runDocs(t, nil, rs, bare)
	wantCodes(t, out, "OS301")
	if !strings.Contains(out, "adopts bare pods") {
		t.Error(out)
	}
	// A Deployment's ReplicaSets don't adopt it, and without an HPA
	// nothing else reads the selector, so it's left alone.
	out, _, _ = runDocs(t, nil, deploy("r-api", "app: api", "app: api", "example/api:1", ""), bare)
	wantCodes(t, out)
}

func TestUnmatchedSelectorIsNotClean(t *testing.T) {
	out, _, code := runDocs(t, nil,
		svc("r-api", apiTpl, "8080"),
		svc("r-other", "app: other", "8080"),
		deploy("r-api", apiTpl, apiTpl, "example/api:1", "{containerPort: 8080}"))
	if code != 1 || !strings.Contains(out, "WARN OS901") || strings.Contains(out, "Result: every selector") {
		t.Errorf("exit %d\n%s", code, out)
	}
	if _, _, code := runDocs(t, []string{"--fail-on", "error"}, svc("r-api", apiTpl, "8080"), svc("r-other", "app: other", "8080"), deploy("r-api", apiTpl, apiTpl, "example/api:1", "{containerPort: 8080}")); code != 0 {
		t.Errorf("--fail-on error: exit %d", code)
	}
}

func TestRefusesBrokenObjects(t *testing.T) {
	good := deploy("r-api", apiTpl, apiTpl, "example/api:1", "{containerPort: 8080}")
	cases := map[string][]string{
		"selector misses own template": {deploy("r-api", "app: other", apiTpl, "example/api:1", "")},
		"no selector":                  {strings.Replace(good, "  selector:\n    matchLabels: {"+apiTpl+"}\n", "", 1)},
		"no containers":                {good[:strings.Index(good, "      containers:")]},
		"no image":                     {strings.Replace(good, "          image: example/api:1\n", "", 1)},
		"bad port":                     {strings.Replace(good, "containerPort: 8080", "containerPort: http", 1)},
		"service no ports":             {good, "apiVersion: v1\nkind: Service\nmetadata:\n  name: s\nspec:\n  selector: {app: api}\n"},
		"pdb no selector":              {good, "apiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata:\n  name: p\nspec:\n  minAvailable: 1\n"},
		"bad operator":                 {good, "apiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata:\n  name: p\nspec:\n  selector:\n    matchExpressions: [{key: app, operator: Has}]\n"},
		"hpa cut":                      {good, "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: h\nspec:\n  scaleTargetRef: {kind: Deployment, name: r-api}\n"},
		"no name":                      {good, "apiVersion: v1\nkind: Service\nmetadata: {}\n"},
		"not an object":                {good, "- a\n- b\n"},
		"no kind":                      {good, "apiVersion: v1\nmetadata:\n  name: x\n"},
		"only services":                {svc("a", api, "80")},
	}
	for name, docs := range cases {
		for _, extra := range [][]string{nil, {"--fail-on", "never"}} {
			out, errs, code := runDocs(t, extra, docs...)
			if code != exitInput || out != "" || !strings.Contains(errs, "can't trust the input") {
				t.Errorf("%s %v: exit %d, stdout %q, stderr %q", name, extra, code, out, errs)
			}
		}
	}
}

func TestHeadlessWithoutPorts(t *testing.T) {
	h := "apiVersion: v1\nkind: Service\nmetadata:\n  name: r-api\nspec:\n  clusterIP: None\n  selector: {" + api + "}\n"
	out, _, _ := runDocs(t, nil, h, deploy("r-api", api, apiTpl, "example/api:1", ""), deploy("r-mail", sink, sink, "m:1", ""))
	wantCodes(t, out, "OS101", "OS301")
	if !strings.Contains(out, "DNS records (headless, no ports)") {
		t.Error(out)
	}
}

func TestListAndJSON(t *testing.T) {
	list := "apiVersion: v1\nkind: List\nitems:\n" +
		"- apiVersion: v1\n  kind: Service\n  metadata: {name: r-api}\n  spec:\n    selector: {app: api}\n    ports: [{port: 80, targetPort: 8080}]\n" +
		"- apiVersion: apps/v1\n  kind: Deployment\n  metadata: {name: a}\n  spec:\n    selector: {matchLabels: {app: api, c: a}}\n    template:\n      metadata: {labels: {app: api, c: a}}\n      spec: {containers: [{name: a, image: a}]}\n" +
		"- apiVersion: apps/v1\n  kind: Deployment\n  metadata: {name: b}\n  spec:\n    selector: {matchLabels: {app: api, c: b}}\n    template:\n      metadata: {labels: {app: api, c: b}}\n      spec: {containers: [{name: b, image: b}]}\n"
	out, _, code := runDocs(t, []string{"--format", "json"}, list)
	if code != 1 || !strings.Contains(out, `"code": "OS101"`) || !strings.Contains(out, `"services": 1`) {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestUsage(t *testing.T) {
	var out, errb bytes.Buffer
	for _, args := range [][]string{{}, {"--format", "xml", "x"}, {"--fail-on", "info", "x"}, {"--nope"}} {
		if code := mainCode(args, strings.NewReader(""), &out, &errb); code != exitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	if code := mainCode([]string{"--version"}, strings.NewReader(""), &out, &errb); code != 0 || !strings.Contains(errb.String(), version) {
		t.Errorf("--version: %d %q", code, errb.String())
	}
}

func TestStdin(t *testing.T) {
	src := svc("r-api", apiTpl, "8080") + "---\n" + deploy("r-api", apiTpl, apiTpl, "example/api:1", "{containerPort: 8080}")
	var out, errb bytes.Buffer
	if code := mainCode([]string{"-"}, strings.NewReader(src), &out, &errb); code != 0 || !strings.Contains(out.String(), "1 Service") {
		t.Errorf("exit %d %s %s", code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := mainCode([]string{"-"}, strings.NewReader(""), &out, &errb); code != exitInput || out.Len() != 0 {
		t.Errorf("empty stdin: exit %d", code)
	}
}

func TestSharedSidecarIsNotSameImage(t *testing.T) {
	// Two different programs with the same sidecar image are not a canary.
	side := "\n        - name: proxy\n          image: example/sidecar:1\n"
	a := strings.TrimSuffix(deploy("r-api", api, apiTpl, "example/api:1", "{containerPort: 8080}"), "\n") + side
	b := strings.TrimSuffix(deploy("r-mail", sink, sink, "axllent/mailpit:1", ""), "\n") + side
	out, _, _ := runDocs(t, nil, svc("r-api", api, "8080"), a, b)
	wantCodes(t, out, "OS101", "OS301")
}
