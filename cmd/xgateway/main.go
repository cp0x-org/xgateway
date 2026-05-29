package main

import (
	"context"
	"log"
	"os"

	gatewayapp "github.com/cp0x-org/xpaywall/xgateway/internal/app"
)

func main() {
	if err := gatewayapp.Run(context.Background(), os.Args); err != nil {
		log.Fatalf("gateway failed: %v", err)
	}
}
