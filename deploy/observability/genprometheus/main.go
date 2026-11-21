// Command genprometheus writes the ims job of
// deploy/observability/prometheus/prometheus.yml from the services of
// the root compose.yaml (deploy/observability/prometheus.go). go
// generate runs it in deploy/observability:
//
//	go generate ./deploy/observability/...
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/yuu-biz/ims-practice/deploy/observability"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genprometheus:", err)
		os.Exit(1)
	}
}

func run() error {
	compose, err := os.ReadFile(observability.RootCompose)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(observability.PrometheusYML)
	if err != nil {
		return err
	}
	content, err := observability.PrometheusConfig(current, compose)
	if err != nil {
		return err
	}
	if bytes.Equal(current, content) {
		return nil
	}
	if err := os.WriteFile(observability.PrometheusYML, content, 0o644); err != nil {
		return err
	}
	fmt.Println("wrote deploy/observability/" + observability.PrometheusYML)
	return nil
}
