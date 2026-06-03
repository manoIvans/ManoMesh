// Package mail abstrai o envio de email transacional. Hoje só temos
// o StubMailer (loga no stdout) — pra plugar Resend/SMTP/SendGrid no
// futuro, basta um novo struct que satisfaça Mailer.
//
// Mesma motivação dos outros stubs do projeto (checkout, gateway): o
// fluxo end-to-end é testável em dev sem dep externa, e a troca é
// só DI no boot do server.
package mail

import (
	"context"
	"fmt"
	"log"
)

// Message é o payload mínimo de um email. To é único (sem CC/BCC no
// MVP; quando precisar, vira []string).
type Message struct {
	To       string
	Subject  string
	HTMLBody string
	TextBody string
}

// Mailer envia emails de forma assíncrona conforme implementação.
// Implementações reais devem ser tolerantes a falha — o caller espera
// que email é best-effort (vide hooks de notificação, mesma filosofia).
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// StubMailer é a implementação default em dev: loga o conteúdo do
// email no stdout. Permite copiar/colar o link de verificação ou
// reset direto do log sem precisar configurar provider real.
//
// Em produção, será trocado por implementação Resend/SMTP via DI no
// server. Stub sempre devolve nil — `Mailer` real vai poder devolver
// erro de rede, mas calller já trata o retorno como best-effort.
type StubMailer struct{}

// NewStubMailer instancia o mailer de dev.
func NewStubMailer() *StubMailer { return &StubMailer{} }

func (StubMailer) Send(_ context.Context, msg Message) error {
	log.Printf("[mail-stub] To=%q Subject=%q\n--- text ---\n%s\n--- end ---\n",
		msg.To, msg.Subject, msg.TextBody)
	return nil
}

// FormatVerificationEmail compõe o email de boas-vindas que sai no
// /register. URL é construída pelo caller (sabe o frontend base) e
// vira o link clicável no template. Centralizar aqui evita drift
// entre as 2 ou 3 chamadas que disparam esse email (register +
// resend-verification).
func FormatVerificationEmail(displayName, verifyURL string) Message {
	subject := "Confirme seu email — ManoMesh"
	text := fmt.Sprintf(
		"Olá, %s!\n\n"+
			"Obrigado por se cadastrar na ManoMesh. Pra completar seu cadastro,\n"+
			"clique no link abaixo (válido por 24 horas):\n\n"+
			"%s\n\n"+
			"Se você não criou esta conta, pode ignorar este email.\n",
		displayName, verifyURL,
	)
	html := fmt.Sprintf(
		`<p>Olá, <strong>%s</strong>!</p>`+
			`<p>Obrigado por se cadastrar na ManoMesh. Pra completar seu cadastro, clique no link abaixo (válido por 24 horas):</p>`+
			`<p><a href="%s">%s</a></p>`+
			`<p style="color:#666;font-size:12px">Se você não criou esta conta, pode ignorar este email.</p>`,
		displayName, verifyURL, verifyURL,
	)
	return Message{Subject: subject, TextBody: text, HTMLBody: html}
}

// FormatPasswordResetEmail compõe o email de reset. URL deve conter
// o token cru — o backend faz hash + lookup no /reset-password.
func FormatPasswordResetEmail(displayName, resetURL string) Message {
	subject := "Redefina sua senha — ManoMesh"
	text := fmt.Sprintf(
		"Olá, %s!\n\n"+
			"Recebemos uma solicitação para redefinir sua senha. Clique no\n"+
			"link abaixo (válido por 1 hora):\n\n"+
			"%s\n\n"+
			"Se você não pediu, pode ignorar este email — sua senha continua a mesma.\n",
		displayName, resetURL,
	)
	html := fmt.Sprintf(
		`<p>Olá, <strong>%s</strong>!</p>`+
			`<p>Recebemos uma solicitação para redefinir sua senha. Clique no link abaixo (válido por 1 hora):</p>`+
			`<p><a href="%s">%s</a></p>`+
			`<p style="color:#666;font-size:12px">Se você não pediu, pode ignorar este email — sua senha continua a mesma.</p>`,
		displayName, resetURL, resetURL,
	)
	return Message{Subject: subject, TextBody: text, HTMLBody: html}
}
