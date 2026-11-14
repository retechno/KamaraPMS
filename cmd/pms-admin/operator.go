package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/operator"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/config"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/subscription"
	"kamarapms/internal/tenancy"
)

const operatorUsage = `
platform console (docs/architecture/39-platform-console.md 6): the accounts of the operators exist only here, there is no screen for them
  pms-admin create-operator -email EMAIL -name NAME [-password-env VAR]   (prints a generated password once; the authenticator is enrolled at the first sign-in)
  pms-admin list-operators
  pms-admin disable-operator -email EMAIL            (ends its sessions)
  pms-admin enable-operator -email EMAIL
  pms-admin reset-operator-password -email EMAIL [-password-env VAR]   (ends its sessions)
  pms-admin reset-operator-totp -email EMAIL         (clears the authenticator and the recovery codes: the next sign-in enrols again)
  pms-admin regenerate-operator-recovery -email EMAIL   (prints 10 new recovery codes once)
  pms-admin rotate-operator-totp-key -old-env VAR -new-env VAR   (re-encrypts every secret in one transaction; then set PMS_OPERATOR_TOTP_KEY to the new key)
  pms-admin suspend-tenant -tenant CODE -reason TEXT
  pms-admin reactivate-tenant -tenant CODE -reason TEXT`

var operatorCommands = map[string]bool{
	"create-operator": true, "list-operators": true, "disable-operator": true, "enable-operator": true, "reset-operator-password": true,
	"reset-operator-totp": true, "regenerate-operator-recovery": true, "rotate-operator-totp-key": true, "suspend-tenant": true, "reactivate-tenant": true,
}

func runOperator(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	email := fs.String("email", "", "e-mail of the operator")
	name := fs.String("name", "", "full name of the operator")
	passwordEnv := fs.String("password-env", "", "read the password from this environment variable instead of generating one")
	oldEnv := fs.String("old-env", "", "environment variable that holds the current key (base64 of 32 bytes)")
	newEnv := fs.String("new-env", "", "environment variable that holds the new key (base64 of 32 bytes)")
	tenantCode := fs.String("tenant", "", "tenant code")
	reason := fs.String("reason", "", "why the tenant is suspended or reactivated (1 to 500 characters)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	txm, cfg, closeDB, err := connectWithConfig(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	aw := audit.NewWriter(clock.System{})
	svc := operator.NewService(txm, clock.System{}, aw, operator.Config{Secret: cfg.JWTSecret, Key: cfg.OperatorTOTPKey})

	switch cmd {
	case "create-operator":
		// Without the key the console is off: an account created now could never sign in.
		if len(cfg.OperatorTOTPKey) == 0 {
			return fmt.Errorf("PMS_OPERATOR_TOTP_KEY is not set, so the console is off and the account could not sign in: set it first (openssl rand -base64 32)")
		}
		password, generated, err := passwordFrom(*passwordEnv)
		if err != nil {
			return err
		}
		op, err := svc.CreateOperator(ctx, *email, *name, password)
		if err != nil {
			return err
		}
		fmt.Printf("created operator %s (id %d)\n", op.Email, op.ID)
		if generated {
			fmt.Printf("password (shown once; the operator can change it only through pms-admin): %s\n", password)
		}
		fmt.Println("the authenticator is enrolled at the first sign-in")
	case "list-operators":
		ops, err := svc.ListOperators(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tE-MAIL\tNAME\tACTIVE\tTOTP\tRECOVERY LEFT\tLAST SIGN-IN")
		for _, o := range ops {
			last := "never"
			if o.LastLoginAt != nil {
				last = o.LastLoginAt.UTC().Format("2006-01-02 15:04 UTC")
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%v\t%v\t%d\t%s\n", o.ID, o.Email, o.FullName, o.IsActive, o.TOTPEnrolled, o.RecoveryCodesLeft, last)
		}
		return w.Flush()
	case "disable-operator", "enable-operator":
		if err := svc.SetActive(ctx, *email, cmd == "enable-operator"); err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", cmd, strings.ToLower(strings.TrimSpace(*email)))
	case "reset-operator-password":
		password, generated, err := passwordFrom(*passwordEnv)
		if err != nil {
			return err
		}
		if err := svc.ResetPassword(ctx, *email, password); err != nil {
			return err
		}
		fmt.Println("password reset; the sessions of the operator are ended")
		if generated {
			fmt.Printf("password (shown once): %s\n", password)
		}
	case "reset-operator-totp":
		if err := svc.ResetTOTP(ctx, *email); err != nil {
			return err
		}
		fmt.Println("authenticator cleared; the sessions are ended and the next sign-in enrols again")
	case "regenerate-operator-recovery":
		codes, err := svc.RegenerateRecovery(ctx, *email)
		if err != nil {
			return err
		}
		fmt.Println("new recovery codes (shown once; the old ones no longer work):")
		for _, c := range codes {
			fmt.Println("  " + c)
		}
	case "rotate-operator-totp-key":
		oldKey, err := keyFromEnv(*oldEnv)
		if err != nil {
			return fmt.Errorf("-old-env: %w", err)
		}
		newKey, err := keyFromEnv(*newEnv)
		if err != nil {
			return fmt.Errorf("-new-env: %w", err)
		}
		n, err := svc.RotateKey(ctx, oldKey, newKey)
		if err != nil {
			return err
		}
		fmt.Printf("re-encrypted %d secrets; now set PMS_OPERATOR_TOTP_KEY to the new key and restart the API\n", n)
	case "suspend-tenant", "reactivate-tenant":
		subs := subscription.NewService(txm, clock.System{}, aw)
		ten := tenancy.NewService(txm, clock.System{}, aw, iam.NewAuthorizer(txm))
		console := operator.NewConsole(txm, clock.System{}, aw, subs, ten, iam.NewService(txm, clock.System{}, aw, iam.TokenConfig{}))
		suspend := cmd == "suspend-tenant"
		// Typing the tenant code on the command line is the confirmation.
		d, err := console.SetSuspended(ctx, *tenantCode, *reason, *tenantCode, suspend)
		if err != nil {
			return err
		}
		if suspend {
			fmt.Printf("tenant %s suspended (%s); its sessions are ended and its users cannot sign in\n", d.Code, strings.TrimSpace(*reason))
		} else {
			fmt.Printf("tenant %s is active again\n", d.Code)
		}
	default:
		return fmt.Errorf("unknown command %q\n%s", cmd, usage)
	}
	return nil
}

// passwordFrom reads the password from the environment variable, or generates one.
func passwordFrom(envName string) (password string, generated bool, err error) {
	if envName != "" {
		return os.Getenv(envName), false, nil
	}
	password, err = operator.GeneratePassword()
	return password, true, err
}

// keyFromEnv reads a key (base64 of 32 bytes) from an environment variable.
func keyFromEnv(name string) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("name the environment variable that holds the key")
	}
	v := os.Getenv(name)
	if v == "" {
		return nil, fmt.Errorf("%s is not set", name)
	}
	return config.ParseOperatorKey(v)
}

// connectWithConfig is connect, and the configuration it read.
func connectWithConfig(ctx context.Context) (*db.TxManager, config.Config, func(), error) {
	if err := config.LoadDotEnv(".env"); err != nil {
		return nil, config.Config{}, nil, err
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, config.Config{}, nil, err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return nil, config.Config{}, nil, err
	}
	return db.NewTxManager(pool, cfg.DBLockTimeout), cfg, pool.Close, nil
}
