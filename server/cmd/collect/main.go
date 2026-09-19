// Command collect samples GitHub for every release in the registry and exits.
// Kubernetes runs it as a daily CronJob; the download chart is built from what
// it records, so a day it does not run is a day with no data.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
