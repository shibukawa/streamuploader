package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"streamuploader/internal/config"
	"streamuploader/internal/server"
	"streamuploader/internal/storage"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := storage.NewS3Store(ctx, storage.S3Config{
		Bucket:         cfg.Bucket,
		Endpoint:       cfg.S3Endpoint,
		Region:         cfg.S3Region,
		AccessKey:      cfg.S3AccessKey,
		SecretKey:      cfg.S3SecretKey,
		ForcePathStyle: cfg.S3ForcePathStyle,
		PublicEndpoint: cfg.S3PublicEndpoint,
		PublicRead:     cfg.S3PublicRead,
	})
	if err != nil {
		log.Fatalf("create s3 store: %v", err)
	}
	log.Printf("streamuploader listening on %s", cfg.Addr)
	if cfg.BackendAddr != "" {
		log.Printf("streamuploader backend control listening on %s", cfg.BackendAddr)
	}
	if err := server.Run(ctx, cfg, store); err != nil {
		log.Fatal(err)
	}
}
