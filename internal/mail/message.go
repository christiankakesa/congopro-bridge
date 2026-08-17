package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

// buildMessage assembles an RFC 5322 message: addresses via net/mail (which
// handles RFC 2047 name encoding and rejects injection), subject as an
// encoded-word, body as quoted-printable UTF-8 so French accents survive
// every hop without relying on 8BITMIME support.
func buildMessage(cfg Config, msg Message) ([]byte, error) {
	if _, err := mail.ParseAddress(msg.To); err != nil {
		return nil, fmt.Errorf("mail: invalid To %q: %w", msg.To, err)
	}

	from := mail.Address{Name: cfg.FromName, Address: cfg.FromAddress}
	to := mail.Address{Address: msg.To}

	id, err := randomID()
	if err != nil {
		return nil, fmt.Errorf("mail: message id: %w", err)
	}

	var buf bytes.Buffer
	buf.Grow(len(msg.Body) + len(msg.Subject) + 512)
	fmt.Fprintf(&buf, "From: %s\r\n", from.String())
	fmt.Fprintf(&buf, "To: %s\r\n", to.String())
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", msg.Subject))
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: <%s-%d@%s>\r\n", id, time.Now().Unix(), domainOf(cfg.FromAddress))
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	buf.WriteString("\r\n")

	// quotedprintable handles line length (76-char soft breaks) and CRLF
	// canonicalization of the body.
	qp := quotedprintable.NewWriter(&buf)
	if _, err := qp.Write([]byte(strings.ReplaceAll(msg.Body, "\n", "\r\n"))); err != nil {
		return nil, fmt.Errorf("mail: encode body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return nil, fmt.Errorf("mail: close body encoder: %w", err)
	}
	return buf.Bytes(), nil
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
