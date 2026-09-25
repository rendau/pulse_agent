// Команда eval — прогон эталонных вопросов агенту (evals/cases.yaml) через API
// POST /v1/ask: таблица в консоль, JSON-отчёт и сравнение с прошлым отчётом.
//
//	go run ./cmd/eval                           # все вопросы, прод-агент через ruto
//	go run ./cmd/eval -only order-found,cluster-errors -baseline eval-report.json
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse_agent/internal/eval"
	"github.com/mechta-market/pulse_agent/internal/infra/httpx"
)

func main() {
	home, _ := os.UserHomeDir()

	casesPath := flag.String("cases", "evals/cases.yaml", "набор вопросов")
	url := flag.String("url", lo.CoalesceOrEmpty(os.Getenv("EVAL_URL"), "https://api.mdev.kz/pulse_agent/v1/ask"), "API агента (EVAL_URL)")
	tokenFile := flag.String("token-file", filepath.Join(home, ".config/pulse_agent/debug_token"), "файл с ключом (DEBUG_TOKEN агента; или EVAL_TOKEN)")
	only := flag.String("only", "", "id вопросов через запятую; пусто — все")
	parallel := flag.Int("parallel", 3, "вопросов одновременно")
	out := flag.String("out", "eval-report.json", "куда сохранить отчёт")
	baselinePath := flag.String("baseline", "", "прошлый отчёт для сравнения")
	chartsDir := flag.String("charts", "", "каталог для картинок графиков")
	flag.Parse()

	if err := run(*casesPath, *url, *tokenFile, *only, *parallel, *out, *baselinePath, *chartsDir); err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(2)
	}
}

func run(casesPath, url, tokenFile, only string, parallel int, out, baselinePath, chartsDir string) error {
	suite, err := eval.Load(casesPath)
	if err != nil {
		return err
	}

	token := os.Getenv("EVAL_TOKEN")
	if token == "" {
		raw, err := os.ReadFile(tokenFile)
		if err != nil {
			return fmt.Errorf("token: set EVAL_TOKEN or %s: %w", tokenFile, err)
		}
		token = strings.TrimSpace(string(raw))
	}

	var baseline *eval.Report
	if baselinePath != "" {
		if baseline, err = eval.LoadReport(baselinePath); err != nil {
			return err
		}
	}

	ids := lo.Filter(strings.Split(only, ","), func(s string, _ int) bool { return strings.TrimSpace(s) != "" })
	now := time.Now()
	cases := lo.Filter(suite.Cases, func(c eval.Case, _ int) bool {
		if len(ids) > 0 && !lo.Contains(ids, c.Id) {
			return false
		}
		if c.Skipped(now) {
			fmt.Printf("⏭  %s: устарел (skip_after %s)\n", c.Id, c.SkipAfter)
			return false
		}
		return true
	})
	if len(cases) == 0 {
		return fmt.Errorf("no cases to run")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	fmt.Printf("Прогон %d вопросов → %s (по %d одновременно)\n\n", len(cases), url, parallel)
	runner := &eval.Runner{
		// ответ приходит целиком после разбора: до таймаута агента (5 мин) с запасом
		Ask:      eval.HttpAsk(url, token, httpx.New(httpx.Config{ResponseHeaderTimeout: 6 * time.Minute, VerifyTLS: true})),
		Parallel: parallel,
		Source:   url,
	}
	report := runner.Run(ctx, cases)
	report.Print(os.Stdout, baseline)

	if chartsDir != "" {
		if err = os.MkdirAll(chartsDir, 0o755); err != nil {
			return fmt.Errorf("charts dir: %w", err)
		}
		if err = report.ChartsTo(chartsDir, base64.StdEncoding.DecodeString); err != nil {
			return err
		}
	}
	if err = report.Save(out); err != nil {
		return err
	}
	fmt.Printf("Отчёт: %s\n", out)

	if report.Failed() {
		os.Exit(1)
	}
	return nil
}
