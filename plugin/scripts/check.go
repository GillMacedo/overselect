package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Finding levels, worst first.
const (
	levelError = "error"
	levelWarn  = "warn"
	levelNote  = "note"
)

type finding struct {
	Code    string   `json:"code"`
	Level   string   `json:"level"`
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Object  string   `json:"object"`
	Summary string   `json:"summary"`
	Details []string `json:"details,omitempty"`
	Fix     string   `json:"fix,omitempty"`
}

func levelRank(l string) int {
	switch l {
	case levelError:
		return 0
	case levelWarn:
		return 1
	}
	return 2
}

// counts says how many selectors were really compared with pod templates.
type counts struct {
	Services     int `json:"services"`
	PDBs         int `json:"podDisruptionBudgets"`
	Selectors    int `json:"workloadSelectors"`
	PodTemplates int `json:"podTemplates"`
}

func (n counts) total() int { return n.Services + n.PDBs + n.Selectors }

func newFinding(code, level string, o *obj, summary string) finding {
	return finding{Code: code, Level: level, File: o.file, Line: o.line, Object: o.kind + " " + nsName(o), Summary: summary}
}

func nsName(o *obj) string {
	if o.ns != "" {
		return o.ns + "/" + o.name
	}
	return o.name
}

func refs(ws []*workload) string {
	var s []string
	for _, w := range ws {
		s = append(s, w.ref())
	}
	return strings.Join(s, ", ")
}

// samePrimary reports whether every workload's first container runs the
// same image repository.
func samePrimary(ws []*workload) bool {
	for _, w := range ws[1:] {
		if w.primary() != ws[0].primary() {
			return false
		}
	}
	return true
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// check compares every selector with every pod template.
func check(c *cluster) ([]finding, counts) {
	n := counts{PodTemplates: len(c.workloads)}
	var out []finding
	for _, s := range c.services {
		f, ok := checkService(c, s)
		if ok {
			n.Services++
		}
		out = append(out, f...)
	}
	f, pdbs := checkPDBs(c)
	n.PDBs = pdbs
	out = append(out, f...)
	f, sels := checkSelectors(c)
	n.Selectors = sels
	out = append(out, f...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if levelRank(a.Level) != levelRank(b.Level) {
			return levelRank(a.Level) < levelRank(b.Level)
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return out, n
}

func noMatch(o *obj, sel *selector) finding {
	f := newFinding("OS901", levelWarn, o, fmt.Sprintf("%s selects no pod template in these files, so it wasn't compared", o.ref()))
	f.Details = []string{"Selector: " + sel.String() + ". Its pods come from somewhere else, such as an operator or a file not passed in, or the files are incomplete."}
	return f
}

// receiver is a workload whose pods get a Service port's traffic.
type receiver struct {
	w      *workload
	detail string
	image  string // the image of the container that gets the traffic
}

// oneImage reports whether every receiver's serving container runs the
// same image repository.
func oneImage(rs []receiver) bool {
	for _, r := range rs[1:] {
		if r.image != rs[0].image {
			return false
		}
	}
	return true
}

func checkService(c *cluster, s *service) ([]finding, bool) {
	sel := c.selected(s.o.ns, s.sel, nil)
	if len(sel) == 0 {
		return []finding{noMatch(s.o, s.sel)}, false
	}
	var mixed, sameImage, jobs []string
	mixedSet := map[*workload]bool{}
	var mixedList, sameList, jobList []*workload
	addTo := func(list []*workload, set map[*workload]bool, ws []*workload) []*workload {
		for _, w := range ws {
			if !set[w] {
				set[w] = true
				list = append(list, w)
			}
		}
		return list
	}
	sameSet, jobSet := map[*workload]bool{}, map[*workload]bool{}
	var mixedPorts []svcPort
	var noPort []string
	for _, p := range s.ports {
		var rs []receiver
		for _, w := range sel {
			if p.dnsOnly {
				rs = append(rs, receiver{w, fmt.Sprintf("%s (%s) gets DNS records", w.ref(), w.primary()), w.primary()})
				continue
			}
			if p.targetStr != "" {
				for _, cp := range w.ports {
					if cp.name == p.targetStr {
						rs = append(rs, receiver{w, fmt.Sprintf("%s (%s) serves it on %d", w.ref(), cp.image, cp.number), cp.image})
						break
					}
				}
				continue
			}
			declared := ""
			for _, cp := range w.ports {
				if cp.number == p.target && declared == "" {
					declared = cp.image
				}
			}
			if declared != "" {
				rs = append(rs, receiver{w, fmt.Sprintf("%s (%s) declares %d", w.ref(), declared, p.target), declared})
			} else {
				rs = append(rs, receiver{w, fmt.Sprintf("%s (%s) doesn't declare %d, so connections to its pods are likely refused", w.ref(), w.primary(), p.target), w.primary()})
			}
		}
		if len(rs) == 0 {
			noPort = append(noPort, p.String())
			continue
		}
		var long, job []*workload
		var longR, jobR []receiver
		for _, r := range rs {
			if r.w.job {
				job, jobR = append(job, r.w), append(jobR, r)
			} else {
				long, longR = append(long, r.w), append(longR, r)
			}
		}
		if len(long) >= 2 && !oneImage(longR) {
			mixed = append(mixed, p.String()+":")
			for _, r := range longR {
				mixed = append(mixed, "  "+r.detail)
			}
			mixedList = addTo(mixedList, mixedSet, long)
			mixedPorts = append(mixedPorts, p)
		} else if len(long) >= 2 {
			sameImage = append(sameImage, p.String()+": "+refs(long))
			sameList = addTo(sameList, sameSet, long)
		}
		if len(job) > 0 && len(long) > 0 {
			jobs = append(jobs, p.String()+":")
			for _, r := range jobR {
				jobs = append(jobs, "  "+r.detail)
			}
			jobList = addTo(jobList, jobSet, job)
		}
	}
	var out []finding
	why := "Selector " + s.sel.String() + " matches all of their pod templates, so the Service spreads requests across their pods."
	if len(mixedList) > 0 {
		f := newFinding("OS101", levelError, s.o, fmt.Sprintf("%s sends traffic to pods of %d workloads that run different images: %s", s.o.ref(), len(mixedList), refs(mixedList)))
		f.Details = append(mixed, why)
		f.Fix = serviceFix(s, mixedList, mixedPorts)
		out = append(out, f)
	}
	if len(jobList) > 0 {
		f := newFinding("OS102", levelWarn, s.o, fmt.Sprintf("%s also sends traffic to the pods of %s while they run", s.o.ref(), refs(jobList)))
		f.Details = append(jobs, "A Job's pods, such as a migration hook, become endpoints like any other pod once they are ready.")
		f.Fix = "Give the Job's pod template labels the Service's selector doesn't match."
		out = append(out, f)
	}
	if len(noPort) > 0 {
		f := newFinding("OS104", levelWarn, s.o, fmt.Sprintf("%s has no endpoints for %s: no pod template it selects has a container port with that name", s.o.ref(), strings.Join(noPort, ", ")))
		f.Details = []string{"It selects " + refs(sel) + ". If the files are complete, the port name is wrong or the container that serves it isn't enabled."}
		f.Fix = "Name the container port the same as the Service's targetPort, or use the port number."
		out = append(out, f)
	}
	if len(sameList) > 0 && len(mixedList) == 0 {
		f := newFinding("OS103", levelNote, s.o, fmt.Sprintf("%s sends traffic to pods of %d workloads that run the same image, which is usually on purpose, as in a canary split or a cluster's headless Service: %s", s.o.ref(), len(sameList), refs(sameList)))
		f.Details = sameImage
		out = append(out, f)
	}
	return out, true
}

// intended guesses which workload a Service is meant for: the one named
// like it, or the only one that declares every target port.
func intended(s *service, ws []*workload, ports []svcPort) *workload {
	var best *workload
	for _, w := range ws {
		if s.o.name == w.o.name || strings.HasPrefix(s.o.name, w.o.name+"-") {
			if best == nil || len(w.o.name) > len(best.o.name) {
				best = w
			}
		}
	}
	if best != nil {
		return best
	}
	var only []*workload
	for _, w := range ws {
		all := true
		for _, p := range ports {
			if p.dnsOnly {
				continue
			}
			found := false
			for _, cp := range w.ports {
				if (p.targetStr != "" && cp.name == p.targetStr) || (p.targetStr == "" && cp.number == p.target) {
					found = true
				}
			}
			all = all && found
		}
		if all {
			only = append(only, w)
		}
	}
	if len(only) == 1 {
		return only[0]
	}
	return nil
}

// distinguishing returns a label on w's pod template whose value none of
// the others has, preferring app.kubernetes.io/component.
func distinguishing(w *workload, others []*workload) (string, string) {
	keys := sortedKeys(w.labels)
	sort.SliceStable(keys, func(i, j int) bool {
		return keys[i] == "app.kubernetes.io/component" && keys[j] != "app.kubernetes.io/component"
	})
	for _, k := range keys {
		ok := true
		for _, o := range others {
			if v, has := o.labels[k]; o != w && has && v == w.labels[k] {
				ok = false
			}
		}
		if ok {
			return k, w.labels[k]
		}
	}
	return "", ""
}

func serviceFix(s *service, ws []*workload, ports []svcPort) string {
	w := intended(s, ws, ports)
	if w == nil {
		return "Give each workload's pod template a label the others don't share, such as app.kubernetes.io/component, and add the right one to the Service's selector."
	}
	if k, v := distinguishing(w, ws); k != "" {
		return fmt.Sprintf("If the Service is meant for %s, add %s=%s to its selector. Only that pod template has it.", w.ref(), k, v)
	}
	return fmt.Sprintf("If the Service is meant for %s, give its pod template a label the others don't have, such as app.kubernetes.io/component, and add it to the Service's selector.", w.ref())
}

func checkPDBs(c *cluster) ([]finding, int) {
	var out []finding
	checked := 0
	cover := map[*workload][]*pdb{}
	var order []*workload
	for _, p := range c.pdbs {
		sel := c.selected(p.o.ns, p.sel, nil)
		if len(sel) == 0 {
			out = append(out, noMatch(p.o, p.sel))
			continue
		}
		checked++
		var long []*workload
		for _, w := range sel {
			if _, ok := cover[w]; !ok {
				order = append(order, w)
			}
			cover[w] = append(cover[w], p)
			if !w.job {
				long = append(long, w)
			}
		}
		if len(long) >= 2 && !samePrimary(long) {
			f := newFinding("OS202", levelWarn, p.o, fmt.Sprintf("%s counts the pods of %d workloads as one budget: %s", p.o.ref(), len(long), refs(long)))
			f.Details = []string{"Selector " + p.sel.String() + " matches all of their pod templates."}
			if p.policy != "" {
				f.Details = append(f.Details, fmt.Sprintf("With %s, pods of one workload can keep the budget met while every pod of another is evicted.", p.policy))
			}
			if p.byOne {
				f.Details = append(f.Details, "Kubernetes documents maxUnavailable and percentage minAvailable only for pods that all have the same controller.")
			}
			f.Fix = "Use one PodDisruptionBudget per workload, with a selector that matches only that workload's pods."
			out = append(out, f)
		}
	}
	for _, w := range order {
		ps := cover[w]
		if len(ps) < 2 {
			continue
		}
		var names []string
		for _, p := range ps {
			names = append(names, p.o.name)
		}
		f := newFinding("OS201", levelError, w.o, fmt.Sprintf("Pods of %s are covered by %d PodDisruptionBudgets: %s", w.ref(), len(ps), strings.Join(names, ", ")))
		f.Details = []string{
			"The eviction API refuses to evict a pod covered by more than one budget (\"This pod has more than one PodDisruptionBudget, which the eviction subresource does not support\"), so kubectl drain and node upgrades that evict through it can't move these pods.",
		}
		for _, p := range ps {
			f.Details = append(f.Details, fmt.Sprintf("%s (%s): selector %s", p.o.ref(), p.o.where(), p.sel))
		}
		f.Fix = "Keep one budget for these pods, or narrow the selectors so each pod matches only one."
		out = append(out, f)
	}
	return out, checked
}

// adopts reports whether a controller of this kind is documented to take
// over bare pods that match its selector.
func adopts(kind string) bool {
	return kind == "ReplicaSet" || kind == "ReplicationController"
}

const overlapWhy = "Kubernetes doesn't stop overlapping selectors, and its docs warn that controllers with overlapping selectors can conflict and behave unexpectedly. Commands that find pods by the selector, such as kubectl logs deployment/NAME and kubectl expose, can pick the other pods."

const overlapFix = "Give each workload a label value the others don't share, such as app.kubernetes.io/component, in its pod template and its selector. spec.selector can't be changed in place, so the workload has to be recreated, for example with kubectl delete --cascade=orphan and then apply."

// hpaLines explains, for each HPA on w, that the others' pods count.
func (c *cluster) hpaLines(w *workload, others []*workload) []string {
	var out []string
	for _, h := range c.hpas {
		if h.kind == w.o.kind && h.target == w.o.name && sameNS(h.o.ns, w.o.ns) {
			out = append(out, fmt.Sprintf("%s scales %s on the metrics of every pod its selector matches, so the pods of %s count too.", h.o.ref(), w.ref(), refs(others)))
		}
	}
	return out
}

func checkSelectors(c *cluster) ([]finding, int) {
	var out []finding
	checked := 0
	index := map[*workload]int{}
	for i, w := range c.workloads {
		index[w] = i
	}
	// matched[w] is every other pod template w's selector matches.
	matched := map[*workload][]*workload{}
	var owners []*workload
	for _, w := range c.workloads {
		if w.sel == nil {
			continue
		}
		peers := 0
		for _, v := range c.workloads {
			if v != w && sameNS(w.o.ns, v.o.ns) {
				peers++
			}
		}
		if peers == 0 {
			continue // nothing else in its namespace to overlap with
		}
		checked++
		owners = append(owners, w)
		matched[w] = c.selected(w.o.ns, w.sel, w)
	}
	key := func(w *workload) string {
		ids := []int{index[w]}
		for _, v := range matched[w] {
			ids = append(ids, index[v])
		}
		sort.Ints(ids)
		return fmt.Sprint(ids)
	}
	// Workloads that all select each other's pods, and nothing else, are
	// reported once as a group.
	grouped := map[*workload]bool{}
	groups := map[string][]*workload{}
	var order []string
	for _, w := range owners {
		if len(matched[w]) == 0 {
			continue
		}
		k := key(w)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], w)
	}
	for _, k := range order {
		g := groups[k]
		if len(g) < 2 || len(g) != len(matched[g[0]])+1 {
			continue
		}
		for _, w := range g {
			grouped[w] = true
		}
		f := newFinding("OS301", levelError, g[0].o, fmt.Sprintf("%d workloads select each other's pods: %s", len(g), refs(g)))
		same := true
		for _, w := range g[1:] {
			same = same && w.sel.String() == g[0].sel.String()
		}
		if same {
			f.Details = append(f.Details, "They all use spec.selector "+g[0].sel.String()+", and every one of their pod templates matches it.")
		} else {
			for _, w := range g {
				f.Details = append(f.Details, fmt.Sprintf("%s (%s): spec.selector %s", w.ref(), w.o.where(), w.sel))
			}
		}
		f.Details = append(f.Details, overlapWhy)
		for _, w := range g {
			var others []*workload
			for _, v := range g {
				if v != w {
					others = append(others, v)
				}
			}
			f.Details = append(f.Details, c.hpaLines(w, others)...)
		}
		f.Fix = overlapFix
		out = append(out, f)
	}
	for _, w := range owners {
		if grouped[w] {
			continue
		}
		for _, v := range matched[w] {
			hpa := c.hpaLines(w, []*workload{v})
			deploy := w.o.kind == "Deployment" || w.o.kind == "Rollout"
			if v.job || (v.o.kind == "Pod" && deploy) {
				// A Job's pods, or a bare pod, which a Deployment's
				// ReplicaSets don't adopt because they also select on
				// pod-template-hash: only an HPA on w is affected.
				if len(hpa) > 0 {
					f := newFinding("OS302", levelWarn, w.o, fmt.Sprintf("spec.selector of %s also matches the pods of %s", w.ref(), v.ref()))
					f.Details = append([]string{fmt.Sprintf("Selector %s matches the pod template of %s (%s).", w.sel, v.ref(), v.o.where())}, hpa...)
					f.Fix = "Give " + v.ref() + "'s pod template labels the selector doesn't match."
					out = append(out, f)
				}
				continue
			}
			f := newFinding("OS301", levelError, w.o, fmt.Sprintf("spec.selector of %s also matches the pods of %s", w.ref(), v.ref()))
			f.Details = []string{fmt.Sprintf("Selector %s matches the pod template of %s (%s).", w.sel, v.ref(), v.o.where())}
			if v.o.kind == "Pod" && adopts(w.o.kind) {
				f.Details = append(f.Details, fmt.Sprintf("A %s adopts bare pods that match its selector, so it can take over %s and delete it to keep its replica count.", w.o.kind, v.ref()))
			} else {
				f.Details = append(f.Details, overlapWhy)
			}
			f.Details = append(f.Details, hpa...)
			f.Fix = overlapFix
			out = append(out, f)
		}
	}
	return out, checked
}
