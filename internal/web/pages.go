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
  <form method="post" action="/login">
    <p><label>Username <input name="name" autocomplete="username" required autofocus></label></p>
    <p><label>Password <input name="password" type="password" autocomplete="current-password" required></label></p>
    <p><button>Log in</button></p>
  </form>`)

	invitePage = page(`  <h1>Set a password for {{.Name}}</h1>
  {{with .Error}}<p role="alert">{{.}}</p>{{end}}
  <form method="post">
    <p><label>Password <input name="password" type="password" autocomplete="new-password" minlength="12" maxlength="128" required autofocus></label></p>
    <p><button>Set password</button></p>
  </form>`)
)
