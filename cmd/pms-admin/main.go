// Command pms-admin performs operator (platform-level) tasks that are not part
// of the hotel-facing API.
//
//	pms-admin create-tenant -code ABC -name "ABC Hotels" -timezone Asia/Jakarta
//	pms-admin show-tenant -code ABC
//	pms-admin create-admin -tenant ABC -email admin@hotel.com -name "Admin"   (prints a generated password)
//
// It reads PMS_DATABASE_URL from the environment or ./.env.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // tenant time zones must resolve even on hosts without a zone database

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/config"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pms-admin:", err)
		os.Exit(1)
	}
}

const usage = `usage:
  pms-admin create-tenant -code CODE -name NAME -timezone ZONE
  pms-admin show-tenant -code CODE
  pms-admin create-admin -tenant CODE -email EMAIL -name NAME [-password-env VAR]`

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	code := fs.String("code", "", "tenant code (A-Z, 0-9, - or _), used at login")
	name := fs.String("name", "", "tenant display name")
	tz := fs.String("timezone", "", "default IANA time zone, e.g. Asia/Jakarta")
	tenantCode := fs.String("tenant", "", "tenant code (create-admin)")
	email := fs.String("email", "", "admin email (create-admin)")
	passwordEnv := fs.String("password-env", "", "read the password from this environment variable instead of generating one")
	if err := fs.Parse(rest); err != nil {
		return err
	}

	if err := config.LoadDotEnv(".env"); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.Open(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()
	txm := db.NewTxManager(pool, cfg.DBLockTimeout)
	aw := audit.NewWriter(clock.System{})
	svc := tenancy.NewService(txm, clock.System{}, aw, iam.NewAuthorizer(txm))

	switch cmd {
	case "create-tenant":
		t, err := svc.CreateTenant(ctx, *code, *name, *tz)
		if err != nil {
			return err
		}
		fmt.Printf("created tenant %s (id %d, time zone %s)\n", t.Code, t.ID, t.Timezone)
		fmt.Printf("next: pms-admin create-admin -tenant %s -email you@example.com -name \"Your Name\"\n", t.Code)
	case "show-tenant":
		t, err := svc.TenantByCode(ctx, *code)
		if err != nil {
			return err
		}
		fmt.Printf("tenant %s: id %d, name %q, status %s, time zone %s\n", t.Code, t.ID, t.Name, t.Status, t.Timezone)
	case "create-admin":
		password, generated := "", false
		if *passwordEnv != "" {
			password = os.Getenv(*passwordEnv)
		} else {
			if password, err = generatePassword(); err != nil {
				return err
			}
			generated = true
		}
		iamSvc := iam.NewService(txm, clock.System{}, aw, iam.TokenConfig{})
		u, err := iamSvc.BootstrapAdmin(ctx, *tenantCode, *email, *name, password)
		if err != nil {
			return err
		}
		fmt.Printf("created tenant administrator %s (id %d) in tenant %s\n", u.Email, u.ID, strings.ToUpper(*tenantCode))
		if generated {
			fmt.Printf("password (shown once; change it after signing in): %s\n", password)
		}
	default:
		return fmt.Errorf("unknown command %q\n%s", cmd, usage)
	}
	return nil
}

// generatePassword returns a random 20-character password (120 bits of entropy).
func generatePassword() (string, error) {
	b := make([]byte, 15)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
