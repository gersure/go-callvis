package main

import (
	"fmt"
	"go/build"
	"go/types"
	"io"
	"os"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"
)

var output io.Writer = os.Stdout

func printOutput(mainPkg *types.Package, cg *callgraph.Graph, focusPkg, limitPath string, ignorePaths []string, groupBy map[string]bool) error {
	groupType := groupBy["type"]
	groupPkg := groupBy["pkg"]

	cluster := NewDotCluster("focus")
	cluster.Attrs = dotAttrs{
		"label":     focusPkg,
		"bgcolor":   "aliceblue",
		"labelloc":  "t",
		"labeljust": "c",
		"fontsize":  "18",
	}

	nodes := []*dotNode{}
	edges := []*dotEdge{}

	nodeMap := make(map[string]*dotNode)
	edgeMap := make(map[string]*dotEdge)

	cg.DeleteSyntheticNodes()

	err := callgraph.GraphVisitEdges(cg, func(edge *callgraph.Edge) error {
		caller := edge.Caller
		callee := edge.Callee

		// omit synthetic calls
		if caller.Func.Pkg == nil || callee.Func.Synthetic != "" {
			return nil
		}

		callerPkg := caller.Func.Pkg.Pkg
		calleePkg := callee.Func.Pkg.Pkg

		// focus specific pkg
		if focusPkg != "" &&
			!(callerPkg.Name() == focusPkg || calleePkg.Name() == focusPkg) {
			return nil
		}

		// limit to path prefix
		if !(strings.HasPrefix(callerPkg.Path(), limitPath) &&
			strings.HasPrefix(calleePkg.Path(), limitPath)) {
			return nil
		}

		// ignore path prefixes
		for _, p := range ignorePaths {
			if strings.HasPrefix(callerPkg.Path(), p) ||
				strings.HasPrefix(calleePkg.Path(), p) {
				return nil
			}
		}

		var sprintNode = func(node *callgraph.Node) *dotNode {
			// only once
			key := node.Func.String()
			if n, ok := nodeMap[key]; ok {
				return n
			}

			attrs := make(dotAttrs)

			// node label
			label := node.Func.RelString(node.Func.Pkg.Pkg)

			// func signature
			sign := node.Func.Signature
			if node.Func.Parent() != nil {
				sign = node.Func.Parent().Signature
			}

			// omit type from label
			if groupType && sign.Recv() != nil {
				parts := strings.Split(label, ".")
				label = parts[len(parts)-1]
			}

			pkg, _ := build.Import(node.Func.Pkg.Pkg.Path(), "", 0)
			// set node color
			if pkg.Goroot {
				attrs["fillcolor"] = "#adedad"
			} else if node.Func.Pkg.Pkg.Name() == focusPkg {
				attrs["fillcolor"] = "lightblue"
			} else {
				attrs["fillcolor"] = "wheat"
			}

			// include pkg name
			if !groupPkg && node.Func.Pkg.Pkg.Name() != focusPkg {
				label = fmt.Sprintf("%s\n%s", node.Func.Pkg.Pkg.Name(), label)
			}

			attrs["label"] = label

			// func styles
			if node.Func.Parent() != nil {
				attrs["style"] = "dotted,filled"
			} else if node.Func.Object() != nil && node.Func.Object().Exported() {
				attrs["penwidth"] = "1.5"
			} else {
				attrs["penwidth"] = "0.5"
			}

			c := cluster

			// group by pkg
			if groupPkg && node.Func.Pkg.Pkg.Name() != focusPkg {
				label := node.Func.Pkg.Pkg.Name()
				if pkg.Goroot {
					label = node.Func.Pkg.Pkg.Path()
				}
				key := node.Func.Pkg.Pkg.Path()
				if _, ok := c.Clusters[key]; !ok {
					c.Clusters[key] = &dotCluster{
						ID:       key,
						Clusters: make(map[string]*dotCluster),
						Attrs: dotAttrs{
							"penwidth":  "0.8",
							"fontsize":  "16",
							"label":     label,
							"style":     "filled",
							"fillcolor": "snow",
						},
					}
					if pkg.Goroot {
						c.Clusters[key].Attrs["fillcolor"] = "#E0FFE1"
					}
				}
				c = c.Clusters[key]
			}

			// group by type
			if groupType && sign.Recv() != nil {
				label := strings.Split(node.Func.RelString(node.Func.Pkg.Pkg), ".")[0]
				fillclr := "lemonchiffon"
				if node.Func.Pkg.Pkg.Name() == focusPkg {
					fillclr = "lavender"
				}
				key := sign.Recv().Type().String()
				if _, ok := c.Clusters[key]; !ok {
					c.Clusters[key] = &dotCluster{
						ID:       key,
						Clusters: make(map[string]*dotCluster),
						Attrs: dotAttrs{
							"penwidth":  "0.5",
							"fontsize":  "15",
							"fontcolor": "#222222",
							"label":     label,
							"labelloc":  "b",
							"style":     "rounded,filled",
							"fillcolor": fillclr,
						},
					}
					if pkg.Goroot {
						c.Clusters[key].Attrs["fillcolor"] = "#c4ecc4"
					}
				}
				c = c.Clusters[key]
			}

			n := &dotNode{
				ID:    node.Func.String(),
				Attrs: attrs,
			}

			if c != nil {
				c.Nodes = append(c.Nodes, n)
			} else {
				nodes = append(nodes, n)
			}

			nodeMap[key] = n
			return n
		}
		callerNode := sprintNode(edge.Caller)
		calleeNode := sprintNode(edge.Callee)

		// edges
		attrs := make(dotAttrs)

		// dynamic call
		if edge.Site != nil && edge.Site.Common().StaticCallee() == nil {
			attrs["style"] = "dashed"
		}

		// go & defer calls
		switch edge.Site.(type) {
		case *ssa.Go:
			attrs["arrowhead"] = "normalnoneodot"
		case *ssa.Defer:
			attrs["arrowhead"] = "normalnoneodiamond"
		}

		// colorize calls outside focused pkg
		if focusPkg != "" &&
			(calleePkg.Name() != focusPkg || callerPkg.Name() != focusPkg) {
			attrs["color"] = "saddlebrown"
		}

		e := &dotEdge{
			From:  callerNode,
			To:    calleeNode,
			Attrs: attrs,
		}

		// omit duplicate calls
		key := fmt.Sprintf("%s = %s => %s", caller.Func, edge.Description(), callee.Func)
		if _, ok := edgeMap[key]; !ok {
			edges = append(edges, e)
			edgeMap[key] = e
		}

		return nil
	})
	if err != nil {
		return err
	}

	logf("%d edges", len(edges))

	dot := &dotGraph{
		Title:   mainPkg.Path(),
		Minlen:  minlen,
		Cluster: cluster,
		Nodes:   nodes,
		Edges:   edges,
		Options: map[string]string{
			"minlen":  fmt.Sprint(minlen),
			"nodesep": fmt.Sprint(nodesep),
		},
	}

	return WriteDot(output, dot)
}
