package notify

import (
	"io"
	"strings"
	"testing"
)

func TestInvitationEmailOrgVsProject(t *testing.T) {
	tests := []struct {
		name        string
		projectName string
		wantInBoth  []string // substrings expected in both HTML and text
		wantSubject string
		isProject   bool
	}{
		{
			name:        "org-level invite",
			projectName: "",
			wantInBoth:  []string{"Acme Inc"},
			wantSubject: "You've been invited to Acme Inc on Vortex",
			isProject:   false,
		},
		{
			name:        "project-level invite",
			projectName: "Website",
			wantInBoth:  []string{"Acme Inc", "Website"},
			wantSubject: "You've been invited to Website on Vortex",
			isProject:   true,
		},
		{
			name:        "whitespace project treated as org-level",
			projectName: "   ",
			wantInBoth:  []string{"Acme Inc"},
			wantSubject: "You've been invited to Acme Inc on Vortex",
			isProject:   false,
		},
	}

	const acceptURL = "https://vortex.dev/invite/accept?token=abc123"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := InvitationEmail("Alice", "Acme Inc", tt.projectName, acceptURL)

			if msg.Subject != tt.wantSubject {
				t.Fatalf("Subject = %q, want %q", msg.Subject, tt.wantSubject)
			}
			if msg.HTMLBody == "" || msg.TextBody == "" {
				t.Fatalf("expected both HTML and text bodies")
			}

			for _, body := range []string{msg.HTMLBody, msg.TextBody} {
				if !strings.Contains(body, "Alice") {
					t.Fatalf("inviter name missing from body:\n%s", body)
				}
				if !strings.Contains(body, acceptURL) {
					t.Fatalf("accept URL missing from body:\n%s", body)
				}
				for _, want := range tt.wantInBoth {
					if !strings.Contains(body, want) {
						t.Fatalf("expected %q in body:\n%s", want, body)
					}
				}
			}

			// Project wording must appear only for project invites.
			projectWordInHTML := strings.Contains(msg.HTMLBody, "the project")
			if projectWordInHTML != tt.isProject {
				t.Fatalf("project wording present=%v, want %v", projectWordInHTML, tt.isProject)
			}
		})
	}
}

func TestInvitationEmailOrgAndProjectBodiesDiffer(t *testing.T) {
	org := InvitationEmail("Alice", "Acme Inc", "", "https://x/y")
	proj := InvitationEmail("Alice", "Acme Inc", "Website", "https://x/y")
	if org.HTMLBody == proj.HTMLBody {
		t.Fatalf("org and project HTML bodies should differ")
	}
	if org.TextBody == proj.TextBody {
		t.Fatalf("org and project text bodies should differ")
	}
	if org.Subject == proj.Subject {
		t.Fatalf("org and project subjects should differ")
	}
}

func TestInvitationEmailEscapesHTML(t *testing.T) {
	evilOrg := `<script>alert('xss')</script>`
	evilName := `Bob<img src=x onerror=alert(1)>`
	msg := InvitationEmail(evilName, evilOrg, "", "https://vortex.dev/a?b=1&c=2")

	if strings.Contains(msg.HTMLBody, "<script>alert('xss')</script>") {
		t.Fatalf("org name not escaped in HTML body:\n%s", msg.HTMLBody)
	}
	if strings.Contains(msg.HTMLBody, "<img src=x") {
		t.Fatalf("inviter name not escaped in HTML body:\n%s", msg.HTMLBody)
	}
	if !strings.Contains(msg.HTMLBody, "&lt;script&gt;") {
		t.Fatalf("expected escaped script tag in HTML body:\n%s", msg.HTMLBody)
	}

	// The plaintext alternative is not HTML and keeps raw characters, which is
	// safe because it is never rendered as HTML.
	if !strings.Contains(msg.TextBody, "<script>alert('xss')</script>") {
		t.Fatalf("plaintext body should preserve raw org name:\n%s", msg.TextBody)
	}
}

func TestInvitationEmailURLPreservedInHref(t *testing.T) {
	url := "https://vortex.dev/invite?token=a-b_c.d"
	msg := InvitationEmail("Alice", "Acme", "", url)
	if !strings.Contains(msg.HTMLBody, `href="`+url+`"`) {
		t.Fatalf("accept URL not used as href:\n%s", msg.HTMLBody)
	}
}

// failingExecutor always returns an error to exercise render's error branch.
type failingExecutor struct{}

func (failingExecutor) Execute(_ io.Writer, _ any) error { return errTemplate }

var errTemplate = errorString("template boom")

type errorString string

func (e errorString) Error() string { return string(e) }

func TestRenderSwallowsExecutionError(t *testing.T) {
	if got := render(failingExecutor{}, nil); got != "" {
		t.Fatalf("render on error = %q, want empty string", got)
	}
}

func TestWelcomeEmail(t *testing.T) {
	msg := WelcomeEmail("Dana")
	if msg.Subject != "Welcome to Vortex" {
		t.Fatalf("Subject = %q", msg.Subject)
	}
	if msg.HTMLBody == "" || msg.TextBody == "" {
		t.Fatalf("expected both bodies")
	}
	for _, body := range []string{msg.HTMLBody, msg.TextBody} {
		if !strings.Contains(body, "Dana") {
			t.Fatalf("name missing from body:\n%s", body)
		}
	}
}

func TestWelcomeEmailEscapesHTML(t *testing.T) {
	msg := WelcomeEmail(`<b>Eve</b>`)
	if strings.Contains(msg.HTMLBody, "<b>Eve</b>") {
		t.Fatalf("name not escaped in HTML body:\n%s", msg.HTMLBody)
	}
	if !strings.Contains(msg.HTMLBody, "&lt;b&gt;Eve&lt;/b&gt;") {
		t.Fatalf("expected escaped name in HTML body:\n%s", msg.HTMLBody)
	}
}

func TestPasswordResetEmail(t *testing.T) {
	url := "https://vortex.example.com/reset-password?token=abc123"
	msg := PasswordResetEmail("Frank", url)
	if msg.Subject != "Reset your Vortex password" {
		t.Fatalf("Subject = %q", msg.Subject)
	}
	if msg.HTMLBody == "" || msg.TextBody == "" {
		t.Fatalf("expected both bodies")
	}
	for _, body := range []string{msg.HTMLBody, msg.TextBody} {
		if !strings.Contains(body, url) {
			t.Fatalf("reset URL missing from body:\n%s", body)
		}
		if !strings.Contains(body, "Frank") {
			t.Fatalf("name missing from body:\n%s", body)
		}
	}
}

func TestPasswordResetEmailEscapesURL(t *testing.T) {
	// The HTML body must auto-escape interpolated values (html/template).
	msg := PasswordResetEmail("Grace", `https://x/?a=1&b=2`)
	if !strings.Contains(msg.HTMLBody, "a=1&amp;b=2") {
		t.Fatalf("expected escaped URL in HTML body:\n%s", msg.HTMLBody)
	}
}
