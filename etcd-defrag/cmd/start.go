package cmd

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/openshift/hypershift/pkg/version"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const namespaceEnvVariableName = "MY_NAMESPACE"

type Options struct {
	CertFile      string
	KeyFile       string
	TrustedCAFile string
}

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "etcd-defrag-controller",
		Short: "Starts the etcd defrag controller",
	}

	opts := Options{
		CertFile:      "/var/run/secrets/etcd-client/etcd-client.crt",
		KeyFile:       "/var/run/secrets/etcd-client/etcd-client.key",
		TrustedCAFile: "/var/run/configmaps/etcd-ca/ca.crt",
	}

	cmd.Flags().StringVar(&opts.CertFile, "cert-file", opts.CertFile, "Path to the serving cert")
	cmd.Flags().StringVar(&opts.KeyFile, "key-file", opts.KeyFile, "Path to the serving key")
	cmd.Flags().StringVar(&opts.TrustedCAFile, "trustedca-file", opts.TrustedCAFile, "Path to the trusted CA file")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := run(ctx, opts); err != nil {
			log.Fatal(err)
		}
	}

	return cmd
}

func run(ctx context.Context, opts Options) error {
	logger := zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
	ctrl.SetLogger(logger)
	logger.Info("Starting etcd-defrag-controller", "version", version.String())

	return nil
}
