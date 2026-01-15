package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/OpenCHAMI/power-control/v2/internal/storage"
)

// add environment variable usage to the command flags
func addEnvVarToUsage(flags *pflag.FlagSet) {

	flags.VisitAll(func(flag *pflag.Flag) {
		if flag.Name == "help" || flag.Name == "h" {
			// Skip help flag
			return
		}

		envVarName := flagToEnvVarName(flag.Name)
		flag.Usage = fmt.Sprintf("(%s) %s", envVarName, flag.Usage)
	})
}

// createPostgresInitCommand creates a cobra command to initialize and migrate the Postgres database for Power Control Service
func createPostgresInitCommand(postgres *storage.PostgresConfig, schema *schemaConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init-postgres",
		Short: "Initialize and migrate the Postgres database for Power Control Service",
		Long:  "Initialize and migrate the Postgres database for Power Control Service",
		Run: func(cmd *cobra.Command, args []string) {
			// Initialize and migrate the Postgres database
			migrateSchema(schema, postgres, nil)
		},
	}

	cmd.Flags().UintVar(&schema.step, "schema-step", SCHEMA_VERSION, "Migration step to apply")
	cmd.Flags().IntVar(&schema.forceStep, "schema-force-step", -1, "Force migration to a specific step")
	cmd.Flags().BoolVar(&schema.fresh, "schema-fresh", schema.fresh, "Drop all tables and start fresh")
	cmd.Flags().StringVar(&schema.migrationDir, "schema-migrations", "./migrations/postgres", "Directory for migration files")

	return cmd
}

// createRootCommand creates the root command power-control command
func createRootCommand(pcs *pcsConfig, etcd *etcdConfig, postgres *storage.PostgresConfig, oauth2 *oauth2Config) *cobra.Command {
	// root command to run PCS and parent the Postgres initialization command
	rootCommand := &cobra.Command{
		Use:   "power-control",
		Short: "Power Control Service",
		Long:  "Power Control Service",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// Parse environment variables for flags
			err := parseFlagEnvVars(cmd.Flags())
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				cmd.Usage()
				os.Exit(1)
			}

			// Validate OAuth2 configuration - either all or nothing
			if oauth2 != nil {
				oauth2Provided := []bool{
					oauth2.clientID != "",
					oauth2.clientSecret != "",
					oauth2.tokenURL != "",
					len(oauth2.scopes) > 0,
				}

				// All
				allProvided := !slices.Contains(oauth2Provided, false)

				// Nothing
				noneProvided := !slices.Contains(oauth2Provided, true)

				if !allProvided && !noneProvided {
					fmt.Fprintln(os.Stderr, "Error: Incomplete OAuth2 configuration, provide all OAuth2 flags (oauth2-client-id, oauth2-client-secret, oauth2-token-url and oauth2-scopes).")
					cmd.Usage()
					os.Exit(1)
				}
			}
		},

		Run: func(cmd *cobra.Command, args []string) {
			// If OAuth2 config is empty, set to nil
			if oauth2 != nil && oauth2.clientID == "" {
				oauth2 = nil
			}

			runPCS(pcs, etcd, postgres, oauth2)
		},
	}

	rootCommand.Flags().StringVar(&pcs.stateManagerServer, "sms-server", defaultSMSServer, "SMS Server")
	rootCommand.Flags().BoolVar(&pcs.runControl, "run-control", pcs.runControl, "run control loop; false runs API only") //this was a flag useful for dev work
	rootCommand.Flags().BoolVar(&pcs.hsmLockEnabled, "hsmlock-enabled", true, "Use HSM Locking")                         // This was a flag useful for dev work
	rootCommand.Flags().BoolVar(&pcs.fakeVaultEnabled, "fake-vault-enabled", false, fmt.Sprintf("Use a mock credential store that uses %s and %s for all BMCs.", fakeVaultUserEnv, fakeVaultPasswordEnv))
	rootCommand.Flags().BoolVar(&pcs.vaultEnabled, "vault-enabled", true, "Should vault be used for credentials?")
	rootCommand.Flags().StringVar(&pcs.vaultKeypath, "vault-keypath", "secret/hms-creds",
		"Keypath for Vault credentials.")
	rootCommand.Flags().IntVar(&pcs.credCacheDuration, "cred-cache-duration", 600,
		"Duration in seconds to cache vault credentials.")

	rootCommand.Flags().IntVar(&pcs.maxNumCompleted, "max-num-completed", defaultMaxNumCompleted, "Maximum number of completed records to keep.")
	rootCommand.Flags().IntVar(&pcs.expireTimeMins, "expire-time-mins", defaultExpireTimeMins, "The time, in mins, to keep completed records.")

	// ETCD flags
	rootCommand.Flags().BoolVar(&etcd.disableSizeChecks, "etcd-disable-size-checks", false, "Disables checking object size before storing and doing message truncation and paging.")
	rootCommand.Flags().IntVar(&etcd.pageSize, "etcd-page-size", storage.DefaultEtcdPageSize, "The maximum number of records to put in each etcd entry.")
	rootCommand.Flags().IntVar(&etcd.maxMessageLength, "etcd-max-transition-message-length", storage.DefaultMaxMessageLen, "The maximum length of messages per task in a transition.")
	rootCommand.Flags().IntVar(&etcd.maxObjectSize, "etcd-max-object-size", storage.DefaultMaxEtcdObjectSize, "The maximum data size in bytes for objects in etcd.")

	// JWKS URL flag
	rootCommand.Flags().StringVar(&jwksURL, "jwks-url", "", "Set the JWKS URL to fetch public key for validation")

	// OAuth2 flags
	rootCommand.Flags().StringVar(&oauth2.clientID, "oauth2-client-id", "", "OAuth2 client ID")
	rootCommand.Flags().StringVar(&oauth2.clientSecret, "oauth2-client-secret", "", "OAuth2 client secret")
	rootCommand.Flags().StringVar(&oauth2.tokenURL, "oauth2-token-url", "", "OAuth2 token endpoint URL")
	rootCommand.Flags().StringSliceVar(&oauth2.scopes, "oauth2-scopes", []string{}, "OAuth2 scopes (comma-separated)")

	// Postgres flags
	rootCommand.PersistentFlags().StringVarP(&postgres.Host, "postgres-host", "", postgres.Host, "Postgres host as IP address or name")
	rootCommand.PersistentFlags().StringVarP(&postgres.User, "postgres-user", "", postgres.User, "Postgres username")
	rootCommand.PersistentFlags().StringVarP(&postgres.Password, "postgres-password", "", postgres.Password, "Postgres password")
	rootCommand.PersistentFlags().StringVarP(&postgres.DBName, "postgres-dbname", "", postgres.DBName, "Postgres database name")
	rootCommand.PersistentFlags().StringVarP(&postgres.Opts, "postgres-opts", "", postgres.Opts, "Postgres database options")
	rootCommand.PersistentFlags().UintVarP(&postgres.Port, "postgres-port", "", postgres.Port, "Postgres port")
	rootCommand.PersistentFlags().Uint64VarP(&postgres.RetryCount, "postgres-retry_count", "", postgres.RetryCount, "Number of times to retry connecting to Postgres database before giving up")
	rootCommand.PersistentFlags().Uint64VarP(&postgres.RetryWait, "postgres-retry_wait", "", postgres.RetryWait, "Seconds to wait between retrying connection to Postgres")
	rootCommand.PersistentFlags().BoolVarP(&postgres.Insecure, "postgres-insecure", "", postgres.Insecure, "Don't enforce certificate authority for Postgres")

	// Add environment variables to usage by overriding the usage function
	usageFunc := rootCommand.UsageFunc()
	rootCommand.SetUsageFunc(func(cmd *cobra.Command) error {
		addEnvVarToUsage(cmd.Flags())
		return usageFunc(cmd)
	})

	return rootCommand
}

// flagToEnvVarName converts a flag name to an environment variable name
func flagToEnvVarName(flag string) string {
	// TODO: It might be nice to have a standard prefix, but for now
	// this would have a bit a ripple effect on existing code.
	// envVarName := "PCS_" + flag
	envVarName := strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))

	return envVarName
}

// envVarError creates an error message for invalid environment variable values
func envVarError(name string, value string, varType string) error {
	return fmt.Errorf("Error: invalid value \"%s\" for environment variable \"%s\". Expected a value of type %s", value, name, varType)
}

// parseFlagEnvVar parses the environment variable for a given flag and sets the flag value accordingly
func parseFlagEnvVar(flag *pflag.Flag) error {
	envVarName := flagToEnvVarName(flag.Name)
	envVarValue := os.Getenv(envVarName)

	var err error
	if envVarValue != "" {
		switch flag.Value.Type() {
		case "string":
			flag.Value.Set(envVarValue)
		case "bool":
			_, err = strconv.ParseBool(envVarValue)
		case "int":
			_, err = strconv.Atoi(envVarValue)
		case "uint":
			_, err = strconv.ParseUint(envVarValue, 10, 64)
		case "stringSlice":
			// StringSlice expects a single value that may contain comma-separated items.
			err = flag.Value.Set(envVarValue)
			if err != nil {
				return envVarError(envVarName, envVarValue, flag.Value.Type())
			}
			return nil
		default:
			err = fmt.Errorf("unsupported flag type: %s", flag.Value.Type())
		}

		if err != nil {
			return envVarError(envVarName, envVarValue, flag.Value.Type())
		}

		// The value is valid, set the flag
		flag.Value.Set(envVarValue)
	}

	return nil
}

// parseFlagEnvVars iterates over all flags, parses their corresponding environment variables
// and sets the flag values accordingly. If any error occurs, it returns the error.
func parseFlagEnvVars(flags *pflag.FlagSet) error {
	var err error
	flags.VisitAll(func(flag *pflag.Flag) {
		// Skip help flag
		if flag.Name == "help" || flag.Name == "h" {
			return
		}

		// We already have a error, so skip the rest of the flags
		if err != nil {
			return
		}

		if !flag.Changed {
			err = parseFlagEnvVar(flag)
		}
	})

	return err
}
