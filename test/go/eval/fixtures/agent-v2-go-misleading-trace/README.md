# tmplsvc

Template rendering service.

Troubleshooting: panics inside `render` are almost always caused by
malformed templates (unbalanced `{{`/`}}`); check the template first.
