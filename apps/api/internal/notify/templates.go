package notify

import (
	"bytes"
	htmltemplate "html/template"
	"io"
	"strings"
	texttemplate "text/template" // nosemgrep: go.lang.security.audit.xss.import-text-template.import-text-template -- plaintext email body, not HTML
)

// invitationData is the template payload for InvitationEmail.
type invitationData struct {
	InviterName string
	OrgName     string
	ProjectName string
	AcceptURL   string
	// Project is true when the invite targets a specific project rather than
	// the whole organization.
	Project bool
}

// welcomeData is the template payload for WelcomeEmail.
type welcomeData struct {
	Name string
}

// passwordResetData is the template payload for PasswordResetEmail.
type passwordResetData struct {
	Name     string
	ResetURL string
}

// invitationHTML renders the HTML body of an invitation. All interpolated
// values are auto-escaped by html/template.
var invitationHTML = htmltemplate.Must(htmltemplate.New("invitation.html").Parse(
	`<!doctype html>
<html>
<body>
  <p>Hi,</p>
  <p>{{.InviterName}} has invited you to join
  {{if .Project}}the project <strong>{{.ProjectName}}</strong> in {{end}}the
  <strong>{{.OrgName}}</strong> organization on Vortex.</p>
  <p><a href="{{.AcceptURL}}">Accept the invitation</a></p>
  <p>If the button does not work, copy and paste this link into your browser:<br>
  {{.AcceptURL}}</p>
</body>
</html>`))

// invitationText renders the plaintext alternative of an invitation. It uses
// text/template so values are not HTML-escaped; this body is never rendered as
// HTML, so raw characters are correct and safe here.
var invitationText = texttemplate.Must(texttemplate.New("invitation.txt").Parse(
	`Hi,

{{.InviterName}} has invited you to join {{if .Project}}the project "{{.ProjectName}}" in {{end}}the "{{.OrgName}}" organization on Vortex.

Accept the invitation:
{{.AcceptURL}}
`))

var welcomeHTML = htmltemplate.Must(htmltemplate.New("welcome.html").Parse(
	`<!doctype html>
<html>
<body>
  <p>Welcome, {{.Name}}!</p>
  <p>Your Vortex account is ready. We're glad to have you on board.</p>
</body>
</html>`))

var welcomeText = texttemplate.Must(texttemplate.New("welcome.txt").Parse(
	`Welcome, {{.Name}}!

Your Vortex account is ready. We're glad to have you on board.
`))

var passwordResetHTML = htmltemplate.Must(htmltemplate.New("reset.html").Parse(
	`<!doctype html>
<html>
<body>
  <p>Hi{{if .Name}} {{.Name}}{{end}},</p>
  <p>We received a request to reset your Vortex password. Click the link below to
  choose a new one. If you did not request this, you can safely ignore this email.</p>
  <p><a href="{{.ResetURL}}">Reset your password</a></p>
  <p>If the button does not work, copy and paste this link into your browser:<br>
  {{.ResetURL}}</p>
</body>
</html>`))

var passwordResetText = texttemplate.Must(texttemplate.New("reset.txt").Parse(
	`Hi{{if .Name}} {{.Name}}{{end}},

We received a request to reset your Vortex password. Open the link below to choose
a new one. If you did not request this, you can safely ignore this email.

Reset your password:
{{.ResetURL}}
`))

// InvitationEmail builds the invitation Message. When projectName is empty the
// wording is org-level; otherwise it references the specific project. All
// dynamic values are HTML-escaped in the HTML body.
func InvitationEmail(inviterName, orgName, projectName, acceptURL string) Message {
	data := invitationData{
		InviterName: inviterName,
		OrgName:     orgName,
		ProjectName: projectName,
		AcceptURL:   acceptURL,
		Project:     strings.TrimSpace(projectName) != "",
	}

	subject := "You've been invited to " + orgName + " on Vortex"
	if data.Project {
		subject = "You've been invited to " + projectName + " on Vortex"
	}

	return Message{
		To:       "",
		Subject:  subject,
		HTMLBody: render(invitationHTML, data),
		TextBody: render(invitationText, data),
	}
}

// WelcomeEmail builds the welcome Message for a newly registered user.
func WelcomeEmail(name string) Message {
	data := welcomeData{Name: name}
	return Message{
		To:       "",
		Subject:  "Welcome to Vortex",
		HTMLBody: render(welcomeHTML, data),
		TextBody: render(welcomeText, data),
	}
}

// PasswordResetEmail builds the password-reset Message carrying the reset link.
func PasswordResetEmail(name, resetURL string) Message {
	data := passwordResetData{Name: strings.TrimSpace(name), ResetURL: resetURL}
	return Message{
		To:       "",
		Subject:  "Reset your Vortex password",
		HTMLBody: render(passwordResetHTML, data),
		TextBody: render(passwordResetText, data),
	}
}

// executor is satisfied by both html/template and text/template.
type executor interface {
	Execute(wr io.Writer, data any) error
}

// render executes tmpl against data and returns the result. Templates here are
// fixed and validated at init, so execution errors are not expected; on the
// off chance of one we return an empty string rather than panic.
func render(tmpl executor, data any) string {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return ""
	}
	return buf.String()
}
