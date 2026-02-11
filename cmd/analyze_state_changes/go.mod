module github.com/deso-protocol/postgres-data-handler/cmd/analyze_state_changes

go 1.23

replace github.com/deso-protocol/core => ../../../../core

replace github.com/deso-protocol/postgres-data-handler => ../..

require (
	github.com/deso-protocol/core v0.0.0
	github.com/spf13/viper v1.19.0
)
