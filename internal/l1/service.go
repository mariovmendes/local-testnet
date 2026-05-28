package l1

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	config "github.com/kurtosis-tech/kurtosis/api/golang/core/lib/starlark_run_config"
	"github.com/kurtosis-tech/kurtosis/api/golang/engine/lib/kurtosis_context"

	_ "embed"
)

//go:embed params.yaml
var params []byte

const (
	enclaveName         = "localnet"
	kurtosisPackageName = "github.com/ssvlabs/ssv-mini"
)

// ANSI colours and styles.
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBlue   = "\033[34m"
	ansiCyan   = "\033[36m"
	ansiRed    = "\033[31m"
	ansiWhite  = "\033[97m"
	ansiBgDark = "\033[48;5;234m"
)

func l1printf(format string, args ...any) {
	fmt.Fprintf(os.Stdout, format, args...)
}

// displayWidth returns the approximate terminal column width of s, counting
// each emoji/wide character as 2 columns and ASCII as 1.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r > 0x2000 { // rough heuristic: CJK, emoji, symbols
			w += 2
		} else {
			w += 1
		}
	}
	return w
}

func l1printBanner(title string) {
	const inner = 56 // columns available between │ delimiters (with 2-space left pad)
	bar := strings.Repeat("─", inner+2)
	l1printf("\n%s┌%s┐%s\n", ansiBold+ansiCyan, bar, ansiReset)
	pad := inner - displayWidth(title)
	if pad < 0 {
		pad = 0
	}
	l1printf("%s│%s  %s%s%s%s│%s\n",
		ansiBold+ansiCyan, ansiReset,
		ansiBold+ansiWhite, title, strings.Repeat(" ", pad),
		ansiBold+ansiCyan, ansiReset)
	l1printf("%s└%s┘%s\n\n", ansiBold+ansiCyan, bar, ansiReset)
}

// isBoringInstruction returns true for instruction descriptions that add no
// value at the terminal (e.g. generic "Printing a message" entries).
func isBoringInstruction(desc string) bool {
	boring := []string{
		"Printing a message",
		"Verifying whether two values",
		"Rendering a template",
	}
	for _, b := range boring {
		if strings.HasPrefix(desc, b) {
			return true
		}
	}
	return false
}

func l1printStep(stepNum, totalSteps uint32, desc string) {
	if totalSteps == 0 || desc == "" {
		return
	}
	// Clean up noisy step-info strings that aren't useful at the terminal.
	skip := []string{
		"Execution in progress",
		"Validating plan",
		"Starting execution",
		"Starting validation",
	}
	for _, s := range skip {
		if strings.HasPrefix(desc, s) {
			return
		}
	}
	l1printf("  %s[%3d/%d]%s %s%s%s\n",
		ansiDim, stepNum, totalSteps, ansiReset,
		ansiBlue, desc, ansiReset)
}

func start(ctx context.Context) error {
	l1printBanner("🚀  Starting L1 Devnet  (Kurtosis)")

	kurtosisCtx, err := kurtosis_context.NewKurtosisContextFromLocalEngine()
	if err != nil {
		return errors.Join(err, errors.New("failed to create kurtosis context"))
	}
	l1printf("  %s●%s  Kurtosis engine connected\n\n", ansiGreen, ansiReset)

	enclaveCtx, err := kurtosisCtx.CreateEnclave(ctx, enclaveName)
	if err != nil {
		return errors.Join(err, errors.New("failed to create enclave"))
	}

	outputCh, cancel, err := enclaveCtx.RunStarlarkRemotePackage(
		ctx,
		kurtosisPackageName,
		config.NewRunStarlarkConfig(config.WithSerializedParams(string(params))))
	if err != nil {
		return errors.Join(err, errors.New("failed to run starlark package"))
	}
	defer cancel()

	var (
		jsonResponse string
		lastStepNum  uint32
	)

	for output := range outputCh {
		// ── Milestone info messages (Step X/5: …) ────────────────────────
		if info := output.GetInfo(); info != nil {
			msg := strings.TrimSpace(info.GetInfoMessage())
			// Suppress Kurtosis self-promotion and other non-operational messages.
			if msg != "" && !strings.Contains(msg, "github.com/kurtosis-tech") {
				l1printf("\n  %s▶  %s%s\n\n", ansiBold+ansiGreen, msg, ansiReset)
			}
		}

		// ── Progress counter ──────────────────────────────────────────────
		if pr := output.GetProgressInfo(); pr != nil {
			stepNum := pr.GetCurrentStepNumber()
			total := pr.GetTotalSteps()
			if stepNum > lastStepNum && stepNum > 0 {
				lastStepNum = stepNum
				// Show the most specific (last) current step description.
				infos := pr.GetCurrentStepInfo()
				if len(infos) > 0 {
					l1printStep(stepNum, total, infos[len(infos)-1])
				}
			}
		}

		// ── Individual instructions (human-readable description) ──────────
		if i := output.GetInstruction(); i != nil {
			desc := strings.TrimSpace(i.GetDescription())
			if desc != "" && !isBoringInstruction(desc) {
				if len(desc) > 120 {
					desc = desc[:117] + "…"
				}
				l1printf("          %s%s%s\n", ansiDim, desc, ansiReset)
			}
		}

		// ── Warnings ──────────────────────────────────────────────────────
		if w := output.GetWarning(); w != nil {
			msg := strings.TrimSpace(w.GetWarningMessage())
			if msg != "" {
				l1printf("  %s⚠%s  %s%s%s\n", ansiBold+ansiYellow, ansiReset, ansiYellow, msg, ansiReset)
			}
		}

		// ── Errors ────────────────────────────────────────────────────────
		if kurtosisErr := output.GetError(); kurtosisErr != nil {
			msg := kurtosisErr.String()
			l1printf("\n  %s✗  Error: %s%s\n\n", ansiBold+ansiRed, msg, ansiReset)
			return fmt.Errorf("kurtosis package returned error: %s", msg)
		}

		// ── Completion ────────────────────────────────────────────────────
		if ev := output.GetRunFinishedEvent(); ev != nil && ev.SerializedOutput != nil {
			jsonResponse = *ev.SerializedOutput
		}
	}

	_ = jsonResponse

	l1printBanner("✅  L1 devnet is running!")
	return nil
}
