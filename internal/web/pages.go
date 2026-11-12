// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import "html/template"

const pageFrame = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>andante</title></head>
<body>
{{block "body" .}}{{end}}
</body>
</html>
`

func page(body string) *template.Template {
	return template.Must(template.Must(template.New("page").Parse(pageFrame)).New("body").Parse(body)).Lookup("page")
}

var (
	loginPage = page(`  <h1>Log in</h1>
  {{with .Error}}<p role="alert">{{.}}</p>{{end}}
  {{if .Password}}<form method="post" action="/login">
    <p><label>Username <input name="name" autocomplete="username" required autofocus></label></p>
    <p><label>Password <input name="password" type="password" autocomplete="current-password" required></label></p>
    <p><button>Log in</button></p>
  </form>{{end}}
  {{if .OIDC}}<p><a href="/login/oidc">Log in with single sign-on</a></p>{{end}}`)

	codePage = page(`  <h1>Two-factor</h1>
  {{with .Error}}<p role="alert">{{.}}</p>{{end}}
  <form method="post" action="/login/code">
    <p><label>The code from your authenticator app, or a recovery code
      <input name="code" autocomplete="one-time-code" required autofocus></label></p>
    <p><button>Log in</button></p>
  </form>`)

	setupPage = page(`  <h1>Set up andante</h1>
  <p>There's no admin yet. The bootstrap token is in the server's log, or it's what --bootstrap-token was set to.</p>
  {{with .Error}}<p role="alert">{{.}}</p>{{end}}
  <form method="post" action="/setup">
    <p><label>Bootstrap token <input name="token" type="password" autocomplete="off" required autofocus></label></p>
    <p><label>Your username <input name="name" value="{{.Name}}" autocomplete="username" maxlength="64" required></label></p>
    <p><button>Carry on</button></p>
  </form>`)

	invitePage = page(`  <h1>Welcome{{with .Name}}, {{.}}{{end}}</h1>
  {{with .Error}}<p role="alert">{{.}}</p>{{end}}
  {{if not .Name}}<form method="post">
    <p><label>Pick a username <input name="name" value="{{.Chosen}}" autocomplete="username" maxlength="64" required autofocus></label></p>
    <p><button>Carry on</button></p>
  </form>{{else}}
  {{if .Password}}<form method="post">{{if .Picked}}<input type="hidden" name="name" value="{{.Name}}">{{end}}
    <p><label>Choose a password <input name="password" type="password" autocomplete="new-password" minlength="12" maxlength="128" required autofocus></label></p>
    <p><button>Set password</button></p>
  </form>{{end}}
  {{if .OIDC}}<p><a href="/invite/{{.Token}}/oidc{{if .Picked}}?name={{.Name}}{{end}}">{{if .Password}}Or use{{else}}Use{{end}} single sign-on</a></p>{{end}}{{end}}`)
)
