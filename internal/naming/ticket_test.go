package naming

import (
	"strings"
	"testing"
)

const (
	defaultBranch  = "ralph/{{if .Ticket}}{{lower .Ticket}}-{{end}}{{lower .ID}}-{{.Slug}}"
	defaultSubject = "{{if .Ticket}}{{.Ticket}} {{end}}{{.ID}}: {{.Title}}"
)

var fixLogin = Data{ID: "T-5", Level: "task", Title: "Fix login", Slug: "fix-login"}

func withTicket(d Data) Data {
	d.Ticket = "ABC-123"
	return d
}

func TestRenderBranchDefaultWithTicket(t *testing.T) {
	got, err := RenderBranch(defaultBranch, withTicket(fixLogin))
	if err != nil || got != "ralph/abc-123-t-5-fix-login" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestRenderBranchDefaultWithoutTicketUnchanged(t *testing.T) {
	got, err := RenderBranch(defaultBranch, fixLogin)
	if err != nil || got != "ralph/t-5-fix-login" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestRenderSubjectDefaultWithTicket(t *testing.T) {
	got, err := RenderSubject(defaultSubject, withTicket(fixLogin))
	if err != nil || got != "ABC-123 T-5: Fix login" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestRenderSubjectDefaultWithoutTicketUnchanged(t *testing.T) {
	got, err := RenderSubject(defaultSubject, fixLogin)
	if err != nil || got != "T-5: Fix login" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestValidateBranchTemplateRendersWithAndWithoutTicket(t *testing.T) {
	if err := ValidateBranchTemplate(defaultBranch); err != nil {
		t.Error(err)
	}
	if err := ValidateBranchTemplate("ralph/{{.Ticket}}-{{lower .ID}}"); err != nil {
		t.Errorf("template using Ticket rejected: %v", err)
	}
}

func TestValidateSubjectTemplateRendersWithAndWithoutTicket(t *testing.T) {
	if err := ValidateSubjectTemplate(defaultSubject); err != nil {
		t.Error(err)
	}
	if err := ValidateSubjectTemplate("{{.Ticket}} {{.ID}}"); err != nil {
		t.Errorf("template using Ticket rejected: %v", err)
	}
	if err := ValidateSubjectTemplate("{{.Ticket.Nope}}"); err == nil {
		t.Error("invalid template accepted")
	}
}

func TestValidateRejectsTemplateInvalidOnlyWithTicket(t *testing.T) {
	const tmpl = "ralph/{{lower .ID}}{{if .Ticket}} {{.Ticket}}{{end}}"
	if _, err := RenderBranch(tmpl, sampleData); err != nil {
		t.Fatalf("fine without a ticket: %v", err)
	}
	err := ValidateBranchTemplate(tmpl)
	if err == nil || !strings.Contains(err.Error(), "ABC-123") {
		t.Errorf("err = %v, want one naming the sample ticket", err)
	}
}

func TestCustomTemplateUntouched(t *testing.T) {
	const tmpl = "feature/{{lower .ID}}"
	got, err := RenderBranch(tmpl, withTicket(fixLogin))
	if err != nil || got != "feature/t-5" {
		t.Errorf("got %q, %v", got, err)
	}
	subj, err := RenderSubject("{{.Title}} ({{.ID}})", withTicket(fixLogin))
	if err != nil || subj != "Fix login (T-5)" {
		t.Errorf("got %q, %v", subj, err)
	}
}
