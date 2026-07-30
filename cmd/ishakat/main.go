package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MichiTrader/ishakat/internal/netfix"
)

var (
	t0      = time.Now()
	version = "0.0.1-spike"
)

const (
	modelsDevURL = "https://models.dev/api.json"
	omniRouteURL = "http://localhost:20128/v1/models"
)

var httpClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
	},
}

func main() {
	rep := netfix.Install()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "doctor":
			os.Exit(doctor(rep))
		case "--version", "-v", "version":
			fmt.Println("ishakat", version)
			return
		default:
			fmt.Fprintf(os.Stderr, "uso: ishakat [doctor|--version]\n")
			os.Exit(2)
		}
	}

	if _, err := tea.NewProgram(spike{rep: rep, boot: time.Since(t0)}).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type probeResult struct {
	Name   string
	Status int
	Bytes  int64
	TTFB   time.Duration
	Total  time.Duration
	Err    error
}

func (p probeResult) line() string {
	if p.Err != nil {
		return fmt.Sprintf("✗ %-11s %v", p.Name, p.Err)
	}
	m := "✗"
	if p.Status == 200 {
		m = "✓"
	}
	return fmt.Sprintf("%s %-11s %d · ttfb %s · total %s · %s",
		m, p.Name, p.Status, short(p.TTFB), short(p.Total), human(p.Bytes))
}

func probe(name, url string, timeout time.Duration) probeResult {
	res := probeResult{Name: name}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		res.Err = err
		return res
	}
	req.Header.Set("User-Agent", "ishakat/"+version)

	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		res.Err = err
		res.Total = time.Since(start)
		return res
	}
	defer resp.Body.Close()

	res.TTFB = time.Since(start)
	res.Status = resp.StatusCode
	res.Bytes, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<20))
	res.Total = time.Since(start)
	if err != nil {
		res.Err = err
	}
	return res
}

func runProbes(timeout time.Duration) []probeResult {
	return []probeResult{
		probe("models.dev", modelsDevURL, timeout),
		probe("omniroute", omniRouteURL, timeout),
	}
}

func doctor(rep netfix.Report) int {
	boot := time.Since(t0)

	fmt.Printf("ishakat %s · doctor\n\n", version)
	fmt.Printf("  go           %s\n", runtime.Version())
	fmt.Printf("  plataforma   %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  cgo          %v\n", netfix.CGOEnabled)
	fmt.Printf("  android      %v\n", rep.Android)
	fmt.Printf("  termux       %v\n", isTermux())
	fmt.Printf("  resolv.conf  %v\n", rep.ResolvConf)
	fmt.Printf("  resolver     %s\n", rep.Resolver())
	if len(rep.Servers) > 0 {
		fmt.Printf("  dns          %s  (%s)\n", strings.Join(rep.Servers, ", "), rep.Source)
	}
	fmt.Printf("  term         TERM=%s COLORTERM=%s\n",
		os.Getenv("TERM"), os.Getenv("COLORTERM"))
	fmt.Println()

	fail := 0
	for _, r := range runProbes(30 * time.Second) {
		fmt.Println("  " + r.line())
		if r.Name == "models.dev" && (r.Err != nil || r.Status != 200) {
			fail = 1
		}
	}

	fmt.Println()
	fmt.Printf("  arranque     %s  (antes de red)\n", short(boot))
	fmt.Printf("  rss          %s\n", human(rssBytes()))
	if !netfix.CGOEnabled && rep.Android {
		fmt.Println("\n  ⚠ binario android sin cgo: netfix está sosteniendo el DNS.")
		fmt.Println("    recompila con CGO_ENABLED=1 y el NDK antes de distribuir.")
	}
	return fail
}

type tickMsg time.Time
type probesMsg []probeResult

type spike struct {
	rep    netfix.Report
	boot   time.Duration
	w, h   int
	busy   bool
	frame  int
	last   []probeResult
	lastCC time.Time
}

func (m spike) Init() tea.Cmd { return nil }

func tick() tea.Cmd {
	return tea.Tick(time.Second/12, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func probeCmd() tea.Cmd {
	return func() tea.Msg { return probesMsg(runProbes(30 * time.Second)) }
}

func (m spike) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		if !m.busy {
			return m, nil
		}
		m.frame++
		return m, tick()

	case probesMsg:
		m.busy = false
		m.last = msg
		cmds := make([]tea.Cmd, 0, len(msg))
		for _, r := range msg {
			cmds = append(cmds, tea.Printf(" %s", r.line()))
		}
		return m, tea.Batch(cmds...)

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			if time.Since(m.lastCC) < time.Second {
				return m, tea.Quit
			}
			m.lastCC = time.Now()
			return m, nil
		case "q":
			return m, tea.Quit
		case "r":
			if m.busy {
				return m, nil
			}
			m.busy = true
			m.frame = 0
			return m, tea.Batch(tick(), probeCmd())
		}
	}
	return m, nil
}

func (m spike) View() tea.View {
	var v tea.View
	v.SetContent(m.render())
	v.AltScreen = false
	v.MouseMode = tea.MouseModeNone
	return v
}

var (
	dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a8580"))
	accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff8a3d"))
)

func (m spike) render() string {
	narrow := m.w > 0 && m.w < 40
	var b strings.Builder

	if !narrow {
		b.WriteString(" " + gradient("ishakat", []string{"#ff6a3d", "#ffa63d", "#ffe0a3"}))
		b.WriteString(dim.Render("  " + version + " · spike"))
		b.WriteString("\n")
	} else {
		b.WriteString(accent.Render(" ishakat") + dim.Render(" spike") + "\n")
	}

	b.WriteString(dim.Render(fmt.Sprintf(" %s/%s · cgo=%v · %s\n",
		runtime.GOOS, runtime.GOARCH, netfix.CGOEnabled, m.rep.Resolver())))
	b.WriteString(dim.Render(fmt.Sprintf(" arranque %s · rss %s · %dx%d\n",
		short(m.boot), human(rssBytes()), m.w, m.h)))

	b.WriteString("\n")
	if m.busy {
		f := []string{"▚▞▘", "▞▘▝", "▘▝▚", "▝▚▗", "▚▗▘", "▗▘▚"}
		b.WriteString(" " + accent.Render(f[m.frame%len(f)]) + dim.Render(" sondeando…\n"))
	} else if m.last == nil {
		b.WriteString(dim.Render(" pulsa r para sondear la red\n"))
	} else {
		b.WriteString(dim.Render(" listo · r repite\n"))
	}

	b.WriteString(dim.Render(" r sondear · ctrl+c×2 o q salir"))
	return b.String()
}

func gradient(s string, stops []string) string {
	rs := []rune(s)
	if len(rs) < 2 || len(stops) < 2 {
		return s
	}
	var out strings.Builder
	for i, r := range rs {
		t := float64(i) / float64(len(rs)-1)
		seg := t * float64(len(stops)-1)
		idx := int(seg)
		if idx >= len(stops)-1 {
			idx = len(stops) - 2
		}
		c := lerpHex(stops[idx], stops[idx+1], seg-float64(idx))
		out.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(string(r)))
	}
	return out.String()
}

func lerpHex(a, b string, t float64) string {
	ar, ag, ab := hex2rgb(a)
	br, bg, bb := hex2rgb(b)
	l := func(x, y int) int { return int(float64(x) + (float64(y)-float64(x))*t) }
	return fmt.Sprintf("#%02x%02x%02x", l(ar, br), l(ag, bg), l(ab, bb))
}

func hex2rgb(h string) (int, int, int) {
	h = strings.TrimPrefix(h, "#")
	if len(h) != 6 {
		return 255, 255, 255
	}
	r, _ := strconv.ParseInt(h[0:2], 16, 32)
	g, _ := strconv.ParseInt(h[2:4], 16, 32)
	b, _ := strconv.ParseInt(h[4:6], 16, 32)
	return int(r), int(g), int(b)
}

func isTermux() bool {
	return os.Getenv("TERMUX_VERSION") != "" ||
		strings.Contains(os.Getenv("PREFIX"), "com.termux")
}

func rssBytes() int64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return int64(ms.Sys)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				kb, _ := strconv.ParseInt(f[1], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}

func short(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}

func human(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f kB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
