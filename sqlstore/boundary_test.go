package sqlstore_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/datafoxlabs/storekit/migrator"
	"github.com/datafoxlabs/storekit/sqlstore"
	"github.com/datafoxlabs/storekit/sqlstore/sqlite"
	"github.com/datafoxlabs/storekit/sqlstore/sqlstoretest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"gorm.io/gorm"
)

func TestOpenRejectsMissingDependenciesBeforeOpeningDatabase(t *testing.T) {
	for _, missing := range []string{"config", "logger", "provider", "driver", "mismatch"} {
		t.Run(missing, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unopened.db")
			cfg := &sqlstore.DatabaseConfig{Type: "sqlite", ConnectionString: path}
			logger := slog.Default()
			var driver sqlstore.Driver = sqlite.Open
			var provider trace.TracerProvider = noop.NewTracerProvider()
			switch missing {
			case "config":
				cfg = nil
			case "logger":
				logger = nil
			case "driver":
				driver = nil
			case "mismatch":
				cfg.Type = "mysql"
			case "provider":
				provider = nil
			}
			store, err := sqlstore.Open(cfg, driver, logger, provider)
			require.Error(t, err)
			require.Nil(t, store)
			_, err = os.Stat(path)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestOpenUsesSuppliedTracerProvider(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := tracesdk.NewTracerProvider(tracesdk.WithSyncer(exporter))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	previous := otel.GetTracerProvider()
	globalExporter := tracetest.NewInMemoryExporter()
	global := tracesdk.NewTracerProvider(tracesdk.WithSyncer(globalExporter))
	otel.SetTracerProvider(global)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, global.Shutdown(context.Background()))
	})
	store, err := sqlstore.Open(&sqlstore.DatabaseConfig{Type: "sqlite", ConnectionString: ":memory:"}, sqlite.Open, slog.Default(), provider)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.NoError(t, store.Exec(context.Background(), func(db *gorm.DB) error {
		return db.Exec("CREATE TABLE provider_test (id INTEGER)").Error
	}))
	require.Same(t, global, otel.GetTracerProvider())
	spans := exporter.GetSpans()
	require.NotEmpty(t, spans)
	scopes := map[string]bool{}
	for _, span := range spans {
		scopes[span.InstrumentationScope.Name] = true
	}
	require.True(t, scopes["gorm"])
	require.True(t, scopes["github.com/datafoxlabs/storekit/sqlstore"])
	require.Empty(t, globalExporter.GetSpans())
	var outer tracetest.SpanStub
	for _, span := range spans {
		if span.Name == "sqlstore.exec" {
			outer = span
		}
	}
	require.True(t, outer.SpanContext.IsValid())
	for _, span := range spans {
		if span.InstrumentationScope.Name == "gorm" {
			require.Equal(t, outer.SpanContext.SpanID(), span.Parent.SpanID())
			require.Equal(t, outer.SpanContext.TraceID(), span.SpanContext.TraceID())
		}
	}
}

func TestStandaloneErrorIdentity(t *testing.T) {
	store := sqlstoretest.New(t, sqlstoretest.WithDBType("sqlite"))
	cause := errors.New("injected migration failure")
	migration := &migrator.Migration{
		ID: "error_identity",
		Up: func(context.Context, migrator.MigrationContext) error { return cause },
	}
	err := store.RunMigrations(context.Background(), migration)
	require.ErrorIs(t, err, sqlstore.ErrMigrationFailed)
	require.ErrorIs(t, err, cause)
	require.NoError(t, store.Close())
	require.ErrorIs(t, store.Ping(context.Background()), sqlstore.ErrPingFailed)
}
