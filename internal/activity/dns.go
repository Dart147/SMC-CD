package activity

import (
	"github.com/Dart147/SMC/deploy/internal/domain"
	"context"

	"go.temporal.io/sdk/activity"
	"go.uber.org/zap"
)

// DNSActivity handles DNS-related activities
type DNSActivity struct {
	dnsProvider domain.DNSProvider
	logger      *zap.Logger
}

// NewDNSActivity creates a new DNS activity
func NewDNSActivity(dnsProvider domain.DNSProvider, logger *zap.Logger) *DNSActivity {
	return &DNSActivity{
		dnsProvider: dnsProvider,
		logger:      logger,
	}
}

// EnsureDNSRecord ensures a DNS A record exists
func (a *DNSActivity) EnsureDNSRecord(ctx context.Context, domain, ip string) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Ensuring DNS record",
		zap.String("domain", domain),
		zap.String("ip", ip),
	)

	if err := a.dnsProvider.EnsureRecord(ctx, domain, ip); err != nil {
		logger.Error("Failed to ensure DNS record",
			zap.Error(err),
			zap.String("domain", domain),
			zap.String("ip", ip),
		)
		return err
	}

	logger.Info("DNS record ensured successfully",
		zap.String("domain", domain),
		zap.String("ip", ip),
	)

	return nil
}

// RemoveDNSRecord removes a DNS A record
func (a *DNSActivity) RemoveDNSRecord(ctx context.Context, domain string) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Removing DNS record",
		zap.String("domain", domain),
	)

	if err := a.dnsProvider.RemoveRecord(ctx, domain); err != nil {
		logger.Error("Failed to remove DNS record",
			zap.Error(err),
			zap.String("domain", domain),
		)
		return err
	}

	logger.Info("DNS record removed successfully",
		zap.String("domain", domain),
	)

	return nil
}
