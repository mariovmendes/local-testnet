// Package logfilter provides line-oriented io.Writer wrappers and line
// transformers that make external subprocess output (op-deployer, forge/just,
// go-ethereum style loggers) readable in a terminal.
package logfilter

import (
	"bufio"
	"bytes"
	"io"
	"regexp"
	"strings"
)

// ── ANSI helpers ─────────────────────────────────────────────────────────────

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
	ansiWhite  = "\033[97m"
)

// ── Line-buffered writer ──────────────────────────────────────────────────────

// lineWriter buffers writes and delivers complete lines to filterFn.
// Incomplete lines (no trailing newline) are flushed when Close is called.
type lineWriter struct {
	w        io.Writer
	buf      bytes.Buffer
	filterFn func(line string) string // returns "" to suppress the line
}

func newLineWriter(w io.Writer, fn func(string) string) *lineWriter {
	return &lineWriter{w: w, filterFn: fn}
}

func (lw *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	lw.buf.Write(p)
	for {
		idx := bytes.IndexByte(lw.buf.Bytes(), '\n')
		if idx < 0 {
			break
		}
		line := string(lw.buf.Next(idx + 1))
		line = strings.TrimRight(line, "\n\r")
		if out := lw.filterFn(line); out != "" {
			_, _ = io.WriteString(lw.w, out+"\n")
		}
	}
	return n, nil
}

// ── op-deployer filter ────────────────────────────────────────────────────────

// go-ethereum log format emitted by op-deployer:
// t=2026-05-28T23:27:43+0100 lvl=info msg="..." key=val key2=val2
// Capture group 2 includes the leading "msg=" so parseGoEthRest can work correctly.
var goEthRe = regexp.MustCompile(`^t=\S+\s+lvl=(\S+)\s+(msg=.+)$`)

// NewDeployerWriter wraps w so that op-deployer output is cleaned up:
//   - callframe / Fault / Revert warnings (expected noise from chain assertions) are suppressed
//   - "Initialized path database", "Running chain assertions", "Setting preinstall" noise is suppressed
//   - "transaction broadcasted" / "Publishing transaction" / "Transaction successfully published" are
//     collapsed — only "transaction confirmed" survives (with concise format)
//   - Everything else is reformatted from go-ethereum key=val to a readable line
func NewDeployerWriter(w io.Writer) io.Writer {
	return newLineWriter(w, filterDeployerLine)
}

// suppressDeployer lists exact msg prefixes to drop entirely.
var suppressDeployer = []string{
	"Initialized path database",
	"Running chain assertions",
	"Setting Multi",
	"Setting Create",
	"Setting Safe",
	"Setting Sender",
	"Setting Entry",
	"Setting Beacon",
	"Setting History",
	"Setting Permit",
	"Setting Deterministic",
	"transaction broadcasted",
	"Publishing transaction",
	"Transaction successfully published",
	"Transaction confirmed",     // lower-case duplicate covered below
	"setting start block",
	"L2 genesis generation not needed",
	"alt-da deployment not needed",
	"additional dispute games deployment not needed",
	"opchain deployment not needed",
	"superchain deployment not needed",
	"implementations deployment not needed",
	"Trie dumping",
	"removing script from state-dump",
	"Transaction receipt not found",
}

// suppressDeployerWarnMsgs are warn-level messages to drop (expected noise).
var suppressDeployerWarnMsgs = []string{
	"Fault",
	"callframe",
	"Revert",
}

func filterDeployerLine(line string) string {
	if line == "" {
		return ""
	}

	m := goEthRe.FindStringSubmatch(line)
	if m == nil {
		// Not go-ethereum format — pass through dimmed.
		return ansiDim + line + ansiReset
	}

	lvlRaw := strings.ToLower(m[1])
	rest := m[2] // everything after lvl=...

	// Parse msg and key=val pairs from rest.
	msg, kvs := parseGoEthRest(rest)

	// Suppress by msg prefix.
	for _, s := range suppressDeployer {
		if strings.HasPrefix(msg, s) {
			return ""
		}
	}
	// Suppress expected warn noise.
	if lvlRaw == "warn" {
		for _, s := range suppressDeployerWarnMsgs {
			if strings.HasPrefix(msg, s) {
				return ""
			}
		}
	}

	// "Failed to create a transaction, will retry" → dim single dot
	if strings.HasPrefix(msg, "Failed to create a transaction") {
		return ansiDim + "    · waiting for next block…" + ansiReset
	}

	// "transaction confirmed" (lower-case) → concise progress
	if msg == "transaction confirmed" {
		completed := kvs["completed"]
		total := kvs["total"]
		hash := kvs["hash"]
		short := ""
		if len(hash) >= 10 {
			short = hash[:10] + "…"
		}
		return ansiDim + "    ✓ tx confirmed  " + completed + "/" + total + "  " + short + ansiReset
	}

	// Format level prefix.
	var lvlStr string
	switch lvlRaw {
	case "error":
		lvlStr = ansiRed + ansiBold + "ERROR" + ansiReset
	case "warn":
		lvlStr = ansiYellow + " WARN" + ansiReset
	default:
		lvlStr = ansiDim + " info" + ansiReset
	}

	// Drop verbose key-value noise for deployer.
	dropKeys := map[string]bool{
		"sender": true, "service": true, "gasTipCap": true,
		"gasFeeCap": true, "gasLimit": true, "nonce": true,
		"tx": true, "id": true, "effectiveGasPrice": true,
		"block": true, "cache": true, "buffer": true,
		"history": true, "readonly": true,
	}

	var kvsOut []string
	for _, k := range kvsOrder(rest) {
		if !dropKeys[k] {
			v := kvs[k]
			if len(v) > 66 { // truncate long hashes/paths
				v = v[:10] + "…"
			}
			kvsOut = append(kvsOut, ansiDim+k+"="+v+ansiReset)
		}
	}

	out := "  " + lvlStr + "  " + ansiWhite + msg + ansiReset
	if len(kvsOut) > 0 {
		out += "  " + strings.Join(kvsOut, "  ")
	}
	return out
}

// parseGoEthRest extracts msg and key=value pairs from the part of a
// go-ethereum log line after `lvl=X `.
// msg may be quoted: msg="foo bar" or unquoted: msg=foo
func parseGoEthRest(rest string) (msg string, kvs map[string]string) {
	kvs = make(map[string]string)
	s := rest

	// Extract msg first.
	if strings.HasPrefix(s, `msg="`) {
		end := strings.Index(s[5:], `"`)
		if end >= 0 {
			msg = s[5 : 5+end]
			s = strings.TrimSpace(s[5+end+1:])
		}
	} else if strings.HasPrefix(s, "msg=") {
		// unquoted msg — ends at first space
		idx := strings.IndexByte(s[4:], ' ')
		if idx >= 0 {
			msg = s[4 : 4+idx]
			s = strings.TrimSpace(s[4+idx:])
		} else {
			msg = s[4:]
			s = ""
		}
	}

	// Remaining: key=val or key="val with spaces"
	for s != "" {
		eqIdx := strings.IndexByte(s, '=')
		if eqIdx < 0 {
			break
		}
		key := s[:eqIdx]
		s = s[eqIdx+1:]
		var val string
		if strings.HasPrefix(s, `"`) {
			end := strings.Index(s[1:], `"`)
			if end >= 0 {
				val = s[1 : 1+end]
				s = strings.TrimSpace(s[1+end+1:])
			} else {
				val = s[1:]
				s = ""
			}
		} else {
			idx := strings.IndexByte(s, ' ')
			if idx >= 0 {
				val = s[:idx]
				s = strings.TrimSpace(s[idx:])
			} else {
				val = s
				s = ""
			}
		}
		kvs[key] = val
	}
	return msg, kvs
}

// kvsOrder returns keys in the order they appear in rest (for consistent output).
func kvsOrder(rest string) []string {
	var keys []string
	seen := map[string]bool{}
	s := rest
	// skip msg field
	if strings.HasPrefix(s, `msg="`) {
		end := strings.Index(s[5:], `"`)
		if end >= 0 {
			s = strings.TrimSpace(s[5+end+1:])
		}
	} else if strings.HasPrefix(s, "msg=") {
		idx := strings.IndexByte(s[4:], ' ')
		if idx >= 0 {
			s = strings.TrimSpace(s[4+idx:])
		} else {
			s = ""
		}
	}
	for s != "" {
		eqIdx := strings.IndexByte(s, '=')
		if eqIdx < 0 {
			break
		}
		key := s[:eqIdx]
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
		s = s[eqIdx+1:]
		if strings.HasPrefix(s, `"`) {
			end := strings.Index(s[1:], `"`)
			if end >= 0 {
				s = strings.TrimSpace(s[1+end+1:])
			} else {
				s = ""
			}
		} else {
			idx := strings.IndexByte(s, ' ')
			if idx >= 0 {
				s = strings.TrimSpace(s[idx:])
			} else {
				s = ""
			}
		}
	}
	return keys
}

// ── Forge / just filter ───────────────────────────────────────────────────────

// NewForgeWriter wraps w with a filter that suppresses the verbose forge/just
// output (gas estimates, file paths, ETHERSCAN warnings, separator lines) and
// highlights the contract deployment results.
func NewForgeWriter(w io.Writer) io.Writer {
	return newLineWriter(w, filterForgeLine)
}

var forgeSep = regexp.MustCompile(`^=+$`)

// suppressForge: if any of these strings appear in a line, drop it.
var suppressForge = []string{
	"Warning: ETHERSCAN_API_KEY",
	"Warning: Skipping verification",
	"No files changed",
	"## Setting up",
	"Estimated gas price",
	"Estimated total gas",
	"Estimated amount required",
	"Transactions saved to",
	"Sensitive values saved to",
	"Script ran successfully",
	"ONCHAIN EXECUTION COMPLETE",
	"==========================",
	"Chain ",
	"Setting up 1 EVM",
	"Deploying Compose Contracts",
}

func filterForgeLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	// Separator lines of `=`
	if forgeSep.MatchString(trimmed) {
		return ""
	}
	for _, s := range suppressForge {
		if strings.Contains(trimmed, s) {
			return ""
		}
	}

	// Step headers
	if strings.HasPrefix(trimmed, "Step ") {
		return "\n  " + ansiCyan + ansiBold + trimmed + ansiReset
	}
	// Deployment success lines
	if strings.HasPrefix(trimmed, "✓ ") || strings.HasPrefix(trimmed, "✓") {
		return "  " + ansiGreen + trimmed + ansiReset
	}
	// Network / RPC info
	if strings.HasPrefix(trimmed, "Network:") || strings.HasPrefix(trimmed, "Chain ID:") ||
		strings.HasPrefix(trimmed, "RPC URL:") {
		return ansiDim + "  " + trimmed + ansiReset
	}
	// Address lines (Implementation:/Proxy:/ProxyAdmin:)
	if strings.HasPrefix(trimmed, "Implementation:") || strings.HasPrefix(trimmed, "Proxy:") ||
		strings.HasPrefix(trimmed, "ProxyAdmin:") || strings.HasPrefix(trimmed, "DisputeGameFactory") ||
		strings.HasPrefix(trimmed, "ComposeL2OutputOracle") || strings.HasPrefix(trimmed, "ComposeDisputeGame") {
		return ansiDim + "    " + trimmed + ansiReset
	}
	// == Logs == / == Return ==
	if trimmed == "== Logs ==" || trimmed == "== Return ==" {
		return ""
	}
	// "Deploying X..." lines inside logs
	if strings.HasPrefix(trimmed, "Deploying ") || strings.HasPrefix(trimmed, "  Deploying ") {
		return ansiDim + "    · " + strings.TrimSpace(trimmed) + ansiReset
	}
	// "X deployed at: 0x..." → keep dim
	if strings.Contains(trimmed, " deployed at:") || strings.Contains(trimmed, " deployed:") {
		return ansiDim + "    " + trimmed + ansiReset
	}
	// ProxyAdmin: 0x...
	if strings.Contains(trimmed, ": 0x") {
		return ansiDim + "    " + trimmed + ansiReset
	}

	return ansiDim + "  " + trimmed + ansiReset
}

// ── Supervisor process line transform ─────────────────────────────────────────

// Timestamps at the start of lines produced by various runtimes.
// go-ethereum: t=2026-05-28T23:29:34+0100
// reth/tracing: 2026-05-28T22:29:34.276810Z
// rollup-boost/sidecar (also reth-style): same
var rethTimestampRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z\s+`)
var goEthTimestampRe = regexp.MustCompile(`^t=\d{4}-\S+\s+`)

// TransformProcessLine reformats a single line from a supervised process so
// that it is consistent and compact regardless of which binary emitted it.
//
//   - go-ethereum format (op-batcher, op-node, op-proposer):
//     "t=... lvl=info msg="foo" key=val"  →  "INFO  foo  key=val"
//   - reth / tracing format (op-reth, op-rbuilder, rollup-boost, sidecar):
//     "2026-...Z  INFO msg …"  →  "INFO  msg …"
//
// The colored process-name prefix is added by the supervisor separately.
func TransformProcessLine(line string) string {
	// go-ethereum format
	if goEthTimestampRe.MatchString(line) {
		// Strip timestamp prefix
		line = goEthTimestampRe.ReplaceAllString(line, "")
		// Now starts with "lvl=X msg=..."
		if strings.HasPrefix(line, "lvl=") {
			spaceIdx := strings.IndexByte(line, ' ')
			if spaceIdx > 0 {
				lvlRaw := strings.ToLower(line[4:spaceIdx])
				rest := line[spaceIdx+1:]
				msg, kvs := parseGoEthRest(rest)

				lvlStr := colorLevel(lvlRaw)
				var kvsOut []string
				dropKV := map[string]bool{"service": true}
				for k, v := range kvs {
					if !dropKV[k] {
						kvsOut = append(kvsOut, ansiDim+k+"="+v+ansiReset)
					}
				}
				out := lvlStr + "  " + msg
				if len(kvsOut) > 0 {
					out += "  " + strings.Join(kvsOut, " ")
				}
				return out
			}
		}
		return line
	}

	// reth / tracing format: strip leading timestamp
	if rethTimestampRe.MatchString(line) {
		line = rethTimestampRe.ReplaceAllString(line, "")
		// line now starts with "INFO/WARN/ERROR/DEBUG ..."
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			lvlRaw := strings.ToLower(parts[0])
			rest := parts[1]
			return colorLevel(lvlRaw) + "  " + rest
		}
	}

	return line
}

func colorLevel(lvl string) string {
	switch lvl {
	case "error":
		return ansiRed + ansiBold + "ERROR" + ansiReset
	case "warn", "warning":
		return ansiYellow + " WARN" + ansiReset
	case "debug":
		return ansiDim + "DEBUG" + ansiReset
	default:
		return ansiCyan + " INFO" + ansiReset
	}
}

// ScanLines reads from r line by line, applying filterFn to each, and writes
// results to w. Useful for piping subprocess output.
func ScanLines(r io.Reader, w io.Writer, filterFn func(string) string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if out := filterFn(line); out != "" {
			_, _ = io.WriteString(w, out+"\n")
		}
	}
}
