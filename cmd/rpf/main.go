package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/7c00/dns-ag/internal/conf"
)

func main() {
	// Find config file with priority order
	configPath, err := conf.GetConfigFile()
	if err != nil {
		log.Fatalf("Failed to find config file: %v", err)
	}

	log.Printf("Using config file: %s", configPath)

	// Load configuration
	config, err := loadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if len(config.Rules) == 0 {
		log.Println("No forwards configured")
		return
	}

	log.Printf("Loaded %d forward rule(s)", len(config.Rules))

	// Create forwarders
	forwarders := make([]*Forwarder, 0, len(config.Rules))
	for i, fw := range config.Rules {
		log.Printf("[%d] Setting up %s: local:%d -> remote:%d",
			i+1, fw.Server, fw.LocalPort, fw.RemotePort)
		forwarder := &Forwarder{rule: fw}

		if err := forwarder.Start(); err != nil {
			log.Printf("[%d] Failed to start %s (local:%d -> remote:%d): %v", i+1, fw.Server, fw.LocalPort, fw.RemotePort, err)
			continue
		}

		forwarders = append(forwarders, forwarder)
	}

	if len(forwarders) == 0 {
		log.Println("No forwarders started successfully")
		return
	}

	// Wait for interrupt signal
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		<-sigChan
		log.Println("\nReceived interrupt signal, shutting down...")
		cancel()
	}()

	// Wait for context cancellation or all forwarders to stop
	done := make(chan struct{})
	go func() {
		for _, f := range forwarders {
			f.wg.Wait()
		}
		close(done)
	}()

	select {
	case <-done:
		log.Println("All forwarders stopped")
	case <-ctx.Done():
		// Stop all forwarders
		for i, f := range forwarders {
			log.Printf("[%d] Stopping forwarder...", i+1)
			f.Stop()
		}
		for _, f := range forwarders {
			f.wg.Wait()
		}
	}

	log.Println("Remote port forward exited")
}
