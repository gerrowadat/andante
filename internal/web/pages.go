// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import "html/template"

const pageFrame = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>andante</title>
<link rel="stylesheet" href="{{root}}/static/vendor/pico-2.1.1/pico.min.css">
<link rel="stylesheet" href="{{root}}/static/style.css">
</head>
<body>
<main class="container narrow">
{{block "body" .}}{{end}}
</main>
</body>
</html>
`

func page(body string) *template.Template {
	return parsed(template.Must(template.Must(template.New("page").Funcs(rootFuncs("")).Parse(pageFrame)).New("body").Parse(body)).Lookup("page"))
}

// templates is every page, as parsed. They're never run as they are: each site
// runs its own copy, whose links start with its root path.
var templates []*template.Template

func parsed(t *template.Template) *template.Template {
	templates = append(templates, t)
	return t
}

// rootFuncs gives a template {{root}}, which every link to a page of ours starts with.
func rootFuncs(root string) template.FuncMap {
	return template.FuncMap{"root": func() string { return root }}
}

// rooted copies every page for a site served from root. A template can't be copied
// once it has run, which is why nothing runs the originals.
func rooted(root string) map[*template.Template]*template.Template {
	views := make(map[*template.Template]*template.Template, len(templates))
	for _, t := range templates {
		views[t] = template.Must(t.Clone()).Funcs(rootFuncs(root))
	}
	return views
}

var (
	loginPage = page(`  <h1>Log in</h1>
  {{with .Error}}<p role="alert">{{.}}</p>{{end}}
  {{if .Password}}<form method="post" action="{{root}}/login">
    <p><label>Username <input name="name" autocomplete="username" required autofocus></label></p>
    <p><label>Password <input name="password" type="password" autocomplete="current-password" required></label></p>
    <p><button>Log in</button></p>
  </form>{{end}}
  {{if .OIDC}}<p><a href="{{root}}/login/oidc">Log in with single sign-on</a></p>{{end}}`)

	codePage = page(`  <h1>Two-factor</h1>
  {{with .Error}}<p role="alert">{{.}}</p>{{end}}
  <form method="post" action="{{root}}/login/code">
    <p><label>The code from your authenticator app, or a recovery code
      <input name="code" autocomplete="one-time-code" required autofocus></label></p>
    <p><button>Log in</button></p>
  </form>`)

	setupPage = page(`  <h1>Set up andante</h1>
  <p>There's no admin yet. The bootstrap token is in the server's log, or it's what --bootstrap-token was set to.</p>
  {{with .Error}}<p role="alert">{{.}}</p>{{end}}
  <form method="post" action="{{root}}/setup">
    <p><label>Bootstrap token <input name="token" type="password" autocomplete="off" required autofocus></label></p>
    <p><label>Your username <input name="name" value="{{.Name}}" autocomplete="username" maxlength="64" required></label></p>
    <p><button>Carry on</button></p>
  </form>`)

	invitePage = page(`  <h1>Welcome{{with .Name}}, {{.}}{{end}}</h1>
  {{with .Error}}<p role="alert">{{.}}</p>{{end}}
  {{if not .Name}}<form method="post" action="{{root}}/invite/{{.Token}}">
    <p><label>Pick a username <input name="name" value="{{.Chosen}}" autocomplete="username" maxlength="64" required autofocus></label></p>
    <p><button>Carry on</button></p>
  </form>{{else}}
  {{if .Password}}<form method="post" action="{{root}}/invite/{{.Token}}">{{if .Picked}}<input type="hidden" name="name" value="{{.Name}}">{{end}}
    <p><label>Choose a password <input name="password" type="password" autocomplete="new-password" minlength="{{.Policy.MinLength}}" maxlength="128" required autofocus></label></p>
    <p>{{.Policy.Rule}} A few words strung together is easy to type and hard to guess.</p>
    <p><button>Set password</button></p>
  </form>{{end}}
  {{if .OIDC}}<p><a href="{{root}}/invite/{{.Token}}/oidc{{if .Picked}}?name={{.Name}}{{end}}">{{if .Password}}Or use{{else}}Use{{end}} single sign-on</a></p>{{end}}{{end}}`)
)
