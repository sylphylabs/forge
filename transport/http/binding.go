package http

import (
	"net/http"
	"net/url"

	"github.com/openkratos/kratos/encoding/form"
	"github.com/openkratos/kratos/errors"
)

func bindQuery(vars url.Values, target any) error {
	if err := form.Unmarshal(vars, target); err != nil {
		return errors.BadRequest("CODEC", err.Error())
	}
	return nil
}

func bindForm(req *http.Request, target any) error {
	if err := req.ParseForm(); err != nil {
		return err
	}
	if err := form.Unmarshal(req.Form, target); err != nil {
		return errors.BadRequest("CODEC", err.Error())
	}
	return nil
}
