package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/font"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

// TODO: remove duplication
type Output struct {
	Name      string
	Snapshots []Snapshot
	Seed      int64
	BaseImage string
}

type Snapshot struct {
	Name        string
	Description string
	HSName      string
	Duration    time.Duration
	MemoryUsage uint64
	CPUUserland uint64
	CPUKernel   uint64
	TxBytes     int64
	RxBytes     int64
}

type PerfRun struct {
	Snapshots []Snapshot
	Name      string
}

func loadFile(filename string) (*PerfRun, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	var s Output
	if err = json.NewDecoder(f).Decode(&s); err != nil {
		return nil, err
	}
	name := filename
	if s.Name != "" {
		name = s.Name
	}
	return &PerfRun{
		Name:      name,
		Snapshots: s.Snapshots,
	}, nil
}

func generateCPUGraph(runs []PerfRun, names []string, filename string) {
	var groups []plotter.Values
	for _, r := range runs {
		var g plotter.Values
		for _, s := range r.Snapshots {
			g = append(g, float64((float64(s.CPUKernel+s.CPUUserland) / float64(time.Millisecond))))
		}
		groups = append(groups, g)
	}

	p := plot.New()
	p.Title.Text = "CPU use over time"
	p.Y.Label.Text = "Total (user+kernel) CPU Time (ms)"

	w := vg.Points(20)
	offsets := make([]font.Length, len(groups))
	switch len(offsets) {
	case 1:
		offsets[0] = 0
	case 2:
		offsets[0] = -0.5 * w
		offsets[1] = 0.5 * w
	case 3:
		offsets[0] = -w
		offsets[1] = 0
		offsets[2] = w
	case 5:
		offsets[0] = -2 * w
		offsets[1] = -w
		offsets[2] = 0
		offsets[3] = w
		offsets[4] = 2 * w
	}

	for i := range groups {
		bars, err := plotter.NewBarChart(groups[i], w)
		if err != nil {
			panic(err)
		}
		bars.LineStyle.Width = vg.Length(0)
		bars.Color = plotutil.Color(i)
		bars.Offset = offsets[i]
		p.Add(bars)
		p.Legend.Add(runs[i].Name, bars)
	}

	p.Legend.Top = true
	p.Legend.Left = true
	p.NominalX(names...)
	p.Add(plotter.NewGrid())

	if err := p.Save(font.Length(float64(len(runs))*float64(len(names))*3*float64(w)), 3*vg.Inch, filename); err != nil {
		panic(err)
	}
}

func generateMemoryGraph(runs []PerfRun, names []string, filename string) {
	var groups []plotter.Values
	for _, r := range runs {
		var g plotter.Values
		for _, s := range r.Snapshots {
			g = append(g, float64((s.MemoryUsage/1024.0)/1024.0))
		}
		groups = append(groups, g)
	}

	p := plot.New()
	p.Title.Text = "Memory use over time"
	p.Y.Label.Text = "Memory (MB)"

	w := vg.Points(20)
	offsets := make([]font.Length, len(groups))
	switch len(offsets) {
	case 1:
		offsets[0] = 0
	case 2:
		offsets[0] = -0.5 * w
		offsets[1] = 0.5 * w
	case 3:
		offsets[0] = -w
		offsets[1] = 0
		offsets[2] = w
	case 5:
		offsets[0] = -2 * w
		offsets[1] = -w
		offsets[2] = 0
		offsets[3] = w
		offsets[4] = 2 * w
	}

	for i := range groups {
		bars, err := plotter.NewBarChart(groups[i], w)
		if err != nil {
			panic(err)
		}
		bars.LineStyle.Width = vg.Length(0)
		bars.Color = plotutil.Color(i)
		bars.Offset = offsets[i]
		p.Add(bars)
		p.Legend.Add(runs[i].Name, bars)
	}

	p.Legend.Top = true
	p.Legend.Left = true
	p.NominalX(names...)
	p.Add(plotter.NewGrid())

	if err := p.Save(font.Length(float64(len(runs))*float64(len(names))*3*float64(w)), 3*vg.Inch, filename); err != nil {
		panic(err)
	}
}

func main() {
	flag.Parse()
	args := flag.Args()
	runs := make([]PerfRun, len(args))
	var names []string
	for i := range runs {
		pr, err := loadFile(args[i])
		if pr == nil {
			fmt.Printf("failed to load snapshot from file '%v' : %v\n", args[i], err)
			os.Exit(2)
		}
		// sanity check that the snapshots are for the same thing
		if names == nil {
			for _, s := range pr.Snapshots {
				names = append(names, s.Name)
			}
		} else {
			for i := range pr.Snapshots {
				if pr.Snapshots[i].Name != names[i] {
					fmt.Printf("snapshots are for different things, cannot make graph: at pos %v  %v != %v", i, pr.Snapshots[i].Name, names[i])
				}
			}
		}
		runs[i] = *pr
	}
	generateMemoryGraph(runs, names, "memory.svg")
	generateCPUGraph(runs, names, "cpu.svg")
	fmt.Println("Output to memory.svg and cpu.svg")
}
