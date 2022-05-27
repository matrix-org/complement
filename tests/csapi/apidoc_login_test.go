package csapi_tests

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestLogin(t *testing.T) {
	idpMux := http.NewServeMux()
	idpURL, idpStop, err := newFakeHTTPServer(idpMux)
	if err != nil {
		t.Fatalf("newFakeHTTPServer failed: %v", err)
	}
	defer idpStop()

	deployment := Deploy(t, b.BlueprintAlice, WithEnv("FAKE_SSO_URL="+idpURL.String()))
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	_ = deployment.RegisterUser(t, "hs1", "test_login_user", "superuser", false)
	t.Run("parallel", func(t *testing.T) {
		// sytest: GET /login yields a set of flows
		t.Run("GET /login yields a set of flows", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(`{}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				JSON: []match.JSON{
					// TODO(paul): Spec is a little vague here. Spec says that every
					//   option needs a 'stages' key, but the implementation omits it
					//   for options that have only one stage in their flow.
					match.JSONArrayEach("flows", func(val gjson.Result) error {
						if !(val.Get("stages").IsArray()) && (val.Get("type").Raw == "") {
							return fmt.Errorf("expected flow to have a list of 'stages' or a 'type' : %v %v", val.Get("type").Raw, val.Get("stages"))
						}
						return nil
					}),
				},
			})
		})
		// sytest: POST /login can log in as a user
		t.Run("POST /login can login as user", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "@test_login_user:hs1"
				},
				"password": "superuser"
			}`))

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyEqual("home_server", "hs1"),
				},
			})
		})
		// sytest: POST /login returns the same device_id as that in the request
		t.Run("POST /login returns the same device_id as that in the request", func(t *testing.T) {
			t.Parallel()
			deviceID := "test_device_id"
			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "@test_login_user:hs1"
				},
				"password": "superuser",
				"device_id": "`+deviceID+`"
			}`))

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyEqual("device_id", deviceID),
				},
			})
		})
		// sytest: POST /login can log in as a user with just the local part of the id
		t.Run("POST /login can log in as a user with just the local part of the id", func(t *testing.T) {
			t.Parallel()

			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "test_login_user"
				},
				"password": "superuser"
			}`))

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyEqual("home_server", "hs1"),
				},
			})
		})
		// sytest: POST /login as non-existing user is rejected
		t.Run("POST /login as non-existing user is rejected", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "i-dont-exist"
				},
				"password": "superuser"
			}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 403,
			})
		})
		// sytest: POST /login wrong password is rejected
		t.Run("POST /login wrong password is rejected", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "@test_login_user:hs1"
				},
				"password": "wrong_password"
			}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 403,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
				},
			})
		})

		// sytest: login types include SSO
		t.Run("login types include SSO", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(`{}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				JSON: []match.JSON{
					match.JSONArraySome("flows", func(val gjson.Result) error {
						if val.Get("type").String() != "m.login.sso" {
							return fmt.Errorf("expected some flow to have an 'm.login.sso' type")
						}
						return nil
					}),
				},
			})
		})
		t.Run("can log in using SSO with OIDC", func(t *testing.T) {
			t.Parallel()
			idpMux.Handle("/.well-known/openid-configuration", jsonHandlerFunc(func(w http.ResponseWriter, r *http.Request) (interface{}, error) {
				return struct {
					Issuer                string `json:"issuer"`
					AuthorizationEndpoint string `json:"authorization_endpoint"`
					TokenEndpoint         string `json:"token_endpoint"`
					UserinfoEndpoint      string `json:"userinfo_endpoint"`
				}{
					Issuer:                "http://issuer.example.com/",
					AuthorizationEndpoint: idpURL.String() + "authorize",
					TokenEndpoint:         idpURL.String() + "token",
					UserinfoEndpoint:      idpURL.String() + "userinfo",
				}, nil
			}))
			hc := *unauthedClient.Client
			hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			}
			var err error
			hc.Jar, err = cookiejar.New(nil)
			if err != nil {
				t.Fatalf("cookiejar.New failed: %v", err)
			}
			cclient := *unauthedClient
			cclient.Client = &hc
			res := cclient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "login", "sso", "redirect", "google"}, client.WithQueries(url.Values{
				"redirectUrl": []string{"http://example.com/continue"},
			}))
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 302,
				HeadersRE: map[string]string{
					"Location": idpURL.String() + `authorize\?client_id=aclientid&redirect_uri=http%3A%2F%2Flocalhost.*%2F_matrix%2Fclient%2Fr0%2Flogin%2Fsso%2Fcallback%3Fprovider%3Dgoogle&response_type=code&scope=openid\+profile\+email&state=.*`,
				},
			})

			authURL, err := url.Parse(res.Header.Get("Location"))
			if err != nil {
				t.Fatalf("url.Parse failed: %v", err)
			}

			idpMux.Handle("/token", jsonHandlerFunc(func(w http.ResponseWriter, r *http.Request) (interface{}, error) {
				if got, want := r.FormValue("code"), "acode"; got != want {
					return nil, fmt.Errorf("code: got %q, want %q", got, want)
				}
				if got, want := r.FormValue("client_id"), "aclientid"; got != want {
					return nil, fmt.Errorf("client_id: got %q, want %q", got, want)
				}
				if got, want := r.FormValue("client_secret"), "aclientsecret"; got != want {
					return nil, fmt.Errorf("client_secret: got %q, want %q", got, want)
				}
				if got, want := r.FormValue("redirect_uri"), "/login/sso/callback?provider=google"; !strings.HasSuffix(got, want) {
					return nil, fmt.Errorf("redirect_uri: got %q, want suffix %q", got, want)
				}
				if got, want := r.FormValue("grant_type"), "authorization_code"; got != want {
					return nil, fmt.Errorf("grant_type: got %q, want %q", got, want)
				}

				return struct {
					AccessToken string `json:"access_token"`
					TokenType   string `json:"token_type"`
				}{
					AccessToken: "anaccesstoken",
					TokenType:   "bearer",
				}, nil
			}))
			idpMux.Handle("/userinfo", jsonHandlerFunc(func(w http.ResponseWriter, r *http.Request) (interface{}, error) {
				if got, want := r.Header.Get("Authorization"), "Bearer anaccesstoken"; got != want {
					return nil, fmt.Errorf("Authorization header: got %q, want %q", got, want)
				}
				if got, want := r.Header.Get("Accept"), "application/json"; got != want {
					return nil, fmt.Errorf("Accept header: got %q, want %q", got, want)
				}

				return struct {
					Sub  string `json:"sub"`
					Name string `json:"name"`
				}{
					Sub:  "asubject",
					Name: "A Name",
				}, nil
			}))

			res = cclient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "login", "sso", "callback"}, client.WithQueries(url.Values{
				"provider": []string{"google"},
				"code":     []string{"acode"},
				"state":    authURL.Query()["state"],
			}))
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 302,
				HeadersRE: map[string]string{
					"Location": `http://example.com/continue\?loginToken=.+`,
				},
			})

			loginURL, err := url.Parse(res.Header.Get("Location"))
			if err != nil {
				t.Fatalf("url.Parse failed: %v", err)
			}

			res = unauthedClient.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(fmt.Sprintf(`{
				"type": "m.login.token",
				"token": "%s"
			}`, loginURL.Query().Get("loginToken")))))
			body := must.ParseJSON(t, res.Body)
			defer res.Body.Close()
			js := gjson.ParseBytes(body)
			unauthedClient.UserID = js.Get("user_id").Str
			unauthedClient.AccessToken = js.Get("access_token").Str
			unauthedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "account", "whoami"})
		})

		// Regression test for https://github.com/matrix-org/dendrite/issues/2287
		t.Run("Login with uppercase username works and GET /whoami afterwards also", func(t *testing.T) {
			t.Parallel()
			// login should be possible with uppercase username
			res := unauthedClient.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "@Test_login_user:hs1"
				},
				"password": "superuser"
			}`)))
			// extract access_token
			body := must.ParseJSON(t, res.Body)
			defer res.Body.Close()
			js := gjson.ParseBytes(body)
			unauthedClient.UserID = js.Get("user_id").Str
			unauthedClient.AccessToken = js.Get("access_token").Str
			// check that we can successfully query /whoami
			unauthedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "account", "whoami"})
		})
	})
}

func newFakeHTTPServer(h http.Handler) (baseURL *url.URL, stop func(), _ error) {
	const dockerInterfaceAddr = ""
	l, err := net.Listen("tcp", dockerInterfaceAddr+":0")
	if err != nil {
		return nil, nil, err
	}

	s := http.Server{
		Addr:    l.Addr().String(),
		Handler: h,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Serve(l)
	}()

	return &url.URL{
			Scheme: "http",
			Host:   fmt.Sprint(docker.HostnameRunningComplement, ":", l.Addr().(*net.TCPAddr).Port),
			Path:   "/",
		}, func() {
			s.Close()
			wg.Wait()
		}, nil
}

func jsonHandlerFunc(fun func(http.ResponseWriter, *http.Request) (interface{}, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, err := fun(w, r)

		if err != nil {
			w.Header().Set("Content-Type", "text/plain")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := json.NewEncoder(w).Encode(resp); err != nil {
			w.Header().Set("Content-Type", "text/plain")
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}
