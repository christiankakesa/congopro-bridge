// mail-test sends one email through the configured SMTP account.
//
// It exists so the OVH EmailPro credentials and the domain's SPF/DKIM/DMARC
// records can be proven working BEFORE anything depends on them (nothing in
// the app sends email yet). Run it with:
//
//	make dev-mail-test TO=you@example.com
//
// For local capture instead of a real mailbox, start Mailpit (make dev-mail-up)
// and override the account: SMTP_HOST=localhost SMTP_PORT=1025 SMTP_TLS=none
// SMTP_USERNAME= SMTP_PASSWORD= — then read the message at
// http://localhost:8025.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/config"
	"congopro-bridge/internal/mail"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: mail-test <to-address>\n\nSends one test email through the SMTP_* configuration from the environment.")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	to := flag.Arg(0)

	cfg := config.Load()
	mailCfg, enabled := cfg.MailConfig()
	if !enabled {
		fmt.Fprintln(os.Stderr, "✗ SMTP_HOST is empty — email is disabled. Set the SMTP_* block in .env (see .env.template).")
		os.Exit(1)
	}
	if err := mailCfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ invalid SMTP configuration: %v\n", err)
		os.Exit(1)
	}

	err := mail.SMTPSender{Config: mailCfg}.Send(mail.Message{
		To:      to,
		Subject: "Congopro Bridge — test email",
		Body: fmt.Sprintf(
			"Ceci est un email de test de Congopro Bridge.\n\n"+
				"Compte : %s:%d (SMTP_TLS=%s)\n"+
				"Si vous lisez ceci, l'envoi et la délivraison fonctionnent — "+
				"vérifiez aussi qu'il n'atterrit pas dans les spams (SPF/DKIM/DMARC).\n",
			mailCfg.Host, mailCfg.Port, mailCfg.TLSMode,
		),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ send failed: %v\n", err)
		os.Exit(1)
	}

	log.Info().Msgf("[mail-test] sent via %s:%d (SMTP_TLS=%s)", mailCfg.Host, mailCfg.Port, mailCfg.TLSMode)
	fmt.Printf("✓ test email sent to %s via %s:%d (SMTP_TLS=%s)\n", to, mailCfg.Host, mailCfg.Port, mailCfg.TLSMode)
	if mailCfg.TLSMode == mail.TLSNone {
		fmt.Println("  (SMTP_TLS=none — local capture: read it at http://localhost:8025)")
	} else {
		fmt.Println("  Check the inbox — and the spam folder: SPF/DKIM/DMARC must all pass for deliverability.")
	}
}
