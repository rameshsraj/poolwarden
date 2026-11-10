package cli

import (
	"github.com/spf13/cobra"
)

// leaf builds one stub command that reports "not implemented".

func addCommands(root *cobra.Command) {
	// Monitoring
	root.AddCommand(
		statusCmd(), nodesCmd(), lagCmd(), slotsCmd(), poolsCmd(), procsCmd(),
		connsCmd(), watchdogCmd(), healthCmd(),
		topCmd(),
	)

	topology := &cobra.Command{Use: "topology", Short: "cluster host inventory and roles", GroupID: "monitoring"}
	topology.AddCommand(
		topologyShowCmd(),
		topologyDiscoverCmd(),
	)
	root.AddCommand(topology, watchCmd(), monitorCmd())

	// Understanding issues
	diagnose := diagnoseCmd()
	explain := explainCmd()

	errs := errorsCmd()
	errs.AddCommand(errorsSummaryCmd(), errorsShowCmd())

	logs := logsCmd()
	root.AddCommand(diagnose, explain, forecastCmd(), errs, logs)

	// Node control
	promote := promoteCmd()
	detach := detachCmd()
	attach := attachCmd()
	recover := recoverCmd()
	reload := reloadCmd()
	root.AddCommand(promote, detach, attach, recover, reload, serviceCmd())

	// Configuration
	cfg := configCmds()
	backend := backendCmds()
	hba := hbaCmds()
	pcpuser := pcpuserCmds()
	pooluser := pooluserCmds()
	sql := sqlCmd()
	root.AddCommand(cfg, backend, hba, pcpuser, pooluser, sql)

	// Audit
	auditC := auditListCmd()
	auditC.Use = "audit"
	auditC.Short = "view the audit log"
	auditC.GroupID = "audit"
	auditC.AddCommand(auditListCmd(), auditShowCmd(), auditVerifyCmd(), auditSessionsCmd())
	root.AddCommand(auditC)

	// Setup
	init := initCmd()
	root.AddCommand(init, probeCmd(), versionCmd())
}
