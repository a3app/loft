package main

import (
	"fmt"
	"strings"
	"time"
)

// renderReport writes report.md: what was tested and where, one summary
// table, and the bootstrap timeline of every case.
func renderReport(m meta, rs []result) string {
	var b strings.Builder
	b.WriteString("# Tor reachability: direct, bridges, and through loft\n\n")

	b.WriteString("| | |\n|---|---|\n")
	row := func(k, v string) {
		if v == "" {
			v = "—"
		}
		fmt.Fprintf(&b, "| %s | %s |\n", k, cell(v))
	}
	row("Started (UTC)", m.Started.Format("2006-01-02 15:04"))
	row("Network", m.Network)
	row("Place", m.Place)
	row("Note", m.Note)
	row("Tool", m.Tool)
	row("OS", m.OS)
	row("tor", m.TorVersion)
	row("Tor Browser", m.TorBrowser)
	row("loft SOCKS5", m.LoftSOCKS)
	b.WriteString("\n")

	b.WriteString("## Summary\n\n")
	b.WriteString("| Case | Result | Bootstrap | Time | Exit check | Onion | Last warning |\n")
	b.WriteString("|---|---|---:|---:|---|---|---|\n")
	for _, r := range rs {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			cell(r.Case.Name), verdict(r), boot(r), dur(r.Took, r.Outcome != "skipped"),
			cell(exitCheck(r)), cell(onionCell(r)), cell(lastWarn(r)))
	}
	b.WriteString("\n")
	b.WriteString("Result: **ok** reached 100% and stopped there; **stuck** did not reach 100% within the time limit; **failed** tor exited first; **skipped** the case could not run here. Exit check: check.torproject.org answered that the request came out of Tor.\n\n")

	b.WriteString("## Cases\n\n")
	for _, r := range rs {
		fmt.Fprintf(&b, "### %s\n\n", r.Case.Name)
		fmt.Fprintf(&b, "%s. Log: [logs/%s](logs/%s)\n\n", r.Case.Describe(), r.LogName(), r.LogName())
		if r.Outcome == "skipped" {
			continue
		}
		if len(r.Steps) > 0 {
			b.WriteString("| Time | % | Stage |\n|---:|---:|---|\n")
			for _, s := range r.Steps {
				fmt.Fprintf(&b, "| %.1fs | %d | %s |\n", s.At.Seconds(), s.Percent, cell(s.Tag+": "+s.Summary))
			}
			b.WriteString("\n")
		} else {
			b.WriteString("No bootstrap progress.\n\n")
		}
		if len(r.Warnings) > 0 {
			b.WriteString("Warnings:\n\n")
			for _, w := range r.Warnings {
				fmt.Fprintf(&b, "- `%s`\n", strings.ReplaceAll(w, "`", "'"))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("---\n\nIP addresses, bridge fingerprints and certificates, and the home folder are replaced in this report and in the logs. Each case used its own tor with an empty data directory.\n")
	return b.String()
}

func verdict(r result) string {
	if r.Outcome == "ok" {
		return "**ok**"
	}
	return r.Outcome
}

func boot(r result) string {
	if r.Outcome == "skipped" {
		return "—"
	}
	return fmt.Sprintf("%d%%", r.Max)
}

func dur(d time.Duration, show bool) string {
	if !show {
		return "—"
	}
	return fmt.Sprintf("%.0fs", d.Seconds())
}

func exitCheck(r result) string {
	if r.Outcome != "ok" {
		return "—"
	}
	return fmt.Sprintf("%s (%.1fs)", r.IsTor, r.CheckIn.Seconds())
}

func onionCell(r result) string {
	if r.Onion == "" {
		return "—"
	}
	return fmt.Sprintf("%s (%.1fs)", r.Onion, r.OnionIn.Seconds())
}

func lastWarn(r result) string {
	if r.Outcome == "skipped" {
		return r.Case.Skip
	}
	if len(r.Warnings) == 0 {
		return "—"
	}
	w := r.Warnings[len(r.Warnings)-1]
	if len(w) > 140 {
		w = w[:140] + "…"
	}
	return w
}

// cell keeps a value from breaking the Markdown table.
func cell(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}
