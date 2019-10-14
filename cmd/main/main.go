package main

import (
	"github.com/qiffang/port-forward/cmd/util"
	"github.com/spf13/cobra"
)

var (
	configPath string
	namespace  string
)

func main() {
	cmd := &cobra.Command{
		Use:     "forward",
		Short:   "support to join TIDB to kubernetes TIDB cluster.",
		Long:    "Port forward TiDB pods and assign virtual ip to pods. External TiDB can join kubernetes TiDB Cluster.",
		Example: "fwdctl forward --path",
		Run: func(cmd *cobra.Command, args []string) {
			util.Start(configPath, namespace)
		},
	}

	cmd.Flags().StringVar(&configPath, "path", "", "Kubernetes config path.")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Kubernetes namespace includes the TiDB cluster you want to join.")
	cmd.MarkFlagRequired("path")
	cmd.MarkFlagRequired("namespace")

	cmd.Execute()
}
