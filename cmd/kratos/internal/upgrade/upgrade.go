package upgrade

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/openkratos/kratos/cmd/kratos/internal/base"
)

// CmdUpgrade represents the upgrade command.
var CmdUpgrade = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade the kratos tools",
	Long:  "Upgrade the kratos tools. Example: kratos upgrade",
	Run:   Run,
}

// Run upgrade the kratos tools.
func Run(_ *cobra.Command, _ []string) {
	err := base.GoInstall(
		"github.com/openkratos/kratos/cmd/kratos@latest",
		"github.com/openkratos/kratos/cmd/protoc-gen-go-http@latest",
		"github.com/openkratos/kratos/cmd/protoc-gen-go-errors@latest",
		"google.golang.org/protobuf/cmd/protoc-gen-go@latest",
		"google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest",
		"github.com/google/gnostic/cmd/protoc-gen-openapi@latest",
	)
	if err != nil {
		fmt.Println(err)
	}
}
