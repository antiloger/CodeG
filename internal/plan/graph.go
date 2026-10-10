package plan

import "sort"

// Group is a set of packages that import each other in a cycle (rare: it needs
// a test variant), or a single package. Packages in a group are processed
// together because their inputs depend on each other.
type Group struct {
	Members []string // sorted
}

// Waves partitions the packages into groups (strongly connected components)
// and orders the groups into waves: every group's dependencies are in earlier
// waves, so groups within a wave are independent and can be loaded together.
//
// imports maps each package to its direct imports; imports of packages not
// in the map are ignored (they are dependencies outside the project).
func Waves(imports map[string][]string) [][]Group {
	nodes := make([]string, 0, len(imports))
	for p := range imports {
		nodes = append(nodes, p)
	}
	sort.Strings(nodes)

	// Tarjan's strongly connected components, iterating in sorted order so
	// the result is deterministic.
	index := map[string]int{}
	low := map[string]int{}
	on := map[string]bool{}
	var stack []string
	var comps [][]string
	next := 0
	var visit func(v string)
	visit = func(v string) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		on[v] = true
		deps := append([]string(nil), imports[v]...)
		sort.Strings(deps)
		for _, w := range deps {
			if _, ok := imports[w]; !ok {
				continue
			}
			if _, seen := index[w]; !seen {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if on[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				on[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sort.Strings(comp)
			comps = append(comps, comp)
		}
	}
	for _, v := range nodes {
		if _, seen := index[v]; !seen {
			visit(v)
		}
	}

	compOf := map[string]int{}
	for i, c := range comps {
		for _, p := range c {
			compOf[p] = i
		}
	}
	// Wave number of a component: one more than its deepest dependency. Tarjan
	// emits components in reverse topological order, so dependencies come first.
	wave := make([]int, len(comps))
	maxWave := 0
	for i, c := range comps {
		for _, p := range c {
			for _, d := range imports[p] {
				if dc, ok := compOf[d]; ok && dc != i {
					wave[i] = max(wave[i], wave[dc]+1)
				}
			}
		}
		maxWave = max(maxWave, wave[i])
	}
	out := make([][]Group, maxWave+1)
	for i, c := range comps {
		out[wave[i]] = append(out[wave[i]], Group{Members: c})
	}
	for _, w := range out {
		sort.Slice(w, func(i, j int) bool { return w[i].Members[0] < w[j].Members[0] })
	}
	if len(comps) == 0 {
		return nil
	}
	return out
}
