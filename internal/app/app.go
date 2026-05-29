package app

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/urfave/cli/v3"

	"github.com/cp0x-org/xpaywall/xgateway/internal/logger"
	"github.com/cp0x-org/xpaywall/xgateway/internal/proxy"
	"github.com/cp0x-org/xpaywall/xgateway/internal/rules"

	appconfig "github.com/cp0x-org/xpaywall/xgateway/config"
	gatewayconfig "github.com/cp0x-org/xpaywall/xgateway/internal/config"
)

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:  "xgateway",
		Usage: "run xpaywall gateway",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "env-file",
				Usage: "path to .env file",
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			return ctx, appconfig.LoadEnv(cmd.String("env-file"))
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_ = cmd

			serviceCfg, err := appconfig.NewGatewayConfig()
			if err != nil {
				return err
			}

			return run(ctx, serviceCfg)
		},
	}
}

func Run(ctx context.Context, args []string) error {
	return NewCommand().Run(ctx, args)
}

func run(ctx context.Context, serviceCfg *appconfig.GatewayConfig) error {
	_ = ctx

	gin.SetMode(serviceCfg.Mode)
	addr := fmt.Sprintf(":%d", serviceCfg.Port)

	provider, opts, err := buildProvider(serviceCfg)
	if err != nil {
		return err
	}

	opts = append(opts, proxy.WithDebug(serviceCfg.Debug))
	srv, err := proxy.New(provider, opts...)
	if err != nil {
		return err
	}
	return srv.Run(addr)
}

func buildProvider(serviceCfg *appconfig.GatewayConfig) (rules.Provider, []proxy.Option, error) {
	if serviceCfg.ConfigProvider == appconfig.ProviderHTTP {
		lg := logger.New(serviceCfg.ControlAPIURL, serviceCfg.InternalAPIKey)
		opts := []proxy.Option{proxy.WithLogger(lg)}
		return rules.NewHttpProvider(serviceCfg.ControlAPIURL, serviceCfg.InternalAPIKey), opts, nil
	}

	cfg, err := gatewayconfig.Load(serviceCfg.ConfigPath)
	if err != nil {
		return nil, nil, err
	}

	var opts []proxy.Option
	if cfg.Outbound.AllowUnmatched {
		fallback, err := proxy.NewReverseProxy(cfg.Outbound.Target, cfg.Outbound.AuthHeader)
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts, proxy.WithFallback(fallback))
	}

	return rules.NewFileProvider(cfg), opts, nil
}
