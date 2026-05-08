package project

import "testing"

// TestResolvedSMTPPort confirms the 587-default and pass-through.
// The default matters because almost every supported provider
// (Resend, SendGrid, Mailgun, AWS SES, Postmark, Brevo, Mailjet)
// uses 587 with STARTTLS — leaving smtpPort blank in mar.json
// should Just Work for them.
func TestResolvedSMTPPort(t *testing.T) {
	cases := []struct {
		name string
		cfg  *MailConfig
		want int
	}{
		{"nil receiver", nil, 587},
		{"empty config", &MailConfig{}, 587},
		{"zero port", &MailConfig{SMTPPort: 0}, 587},
		{"explicit 587", &MailConfig{SMTPPort: 587}, 587},
		{"implicit-TLS 465", &MailConfig{SMTPPort: 465}, 465},
		{"legacy 25", &MailConfig{SMTPPort: 25}, 25},
		{"alternative 2525", &MailConfig{SMTPPort: 2525}, 2525},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.ResolvedSMTPPort()
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestToSMTPConfig validates the manifest → runtime conversion.
// A nil/empty manifest yields zero config (so dev's stdout-sink
// path activates); a populated manifest yields the right host/port/
// auth values, with the port-default applied automatically.
func TestToSMTPConfig(t *testing.T) {
	t.Run("nil manifest yields zero config", func(t *testing.T) {
		got := ToSMTPConfig(nil)
		if got.Host != "" {
			t.Errorf("Host: got %q, want empty", got.Host)
		}
	})

	t.Run("manifest without mail block yields zero config", func(t *testing.T) {
		got := ToSMTPConfig(&Manifest{Name: "x"})
		if got.Host != "" {
			t.Errorf("Host: got %q, want empty", got.Host)
		}
	})

	t.Run("default port applied when smtpPort missing", func(t *testing.T) {
		got := ToSMTPConfig(&Manifest{
			Mail: &MailConfig{
				SMTPHost:     "smtp.resend.com",
				SMTPUsername: "resend",
				SMTPPassword: "re_xxx",
			},
		})
		if got.Port != 587 {
			t.Errorf("Port: got %d, want %d", got.Port, 587)
		}
		if got.Host != "smtp.resend.com" {
			t.Errorf("Host: got %q, want %q", got.Host, "smtp.resend.com")
		}
	})

	t.Run("explicit port preserved", func(t *testing.T) {
		got := ToSMTPConfig(&Manifest{
			Mail: &MailConfig{
				SMTPHost: "smtp.aws.com",
				SMTPPort: 465,
			},
		})
		if got.Port != 465 {
			t.Errorf("Port: got %d, want %d", got.Port, 465)
		}
	})
}
