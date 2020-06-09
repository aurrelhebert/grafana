package backendplugin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/stretchr/testify/require"
)

const testPluginID = "test-plugin"

func TestManager(t *testing.T) {
	newManagerScenario(t, false, func(t *testing.T, ctx *managerScenarioCtx) {
		t.Run("Unregistered plugin scenario", func(t *testing.T) {
			err := ctx.manager.StartPlugin(context.Background(), testPluginID)
			require.Equal(t, ErrPluginNotRegistered, err)

			_, err = ctx.manager.CollectMetrics(context.Background(), testPluginID)
			require.Equal(t, ErrPluginNotRegistered, err)

			_, err = ctx.manager.CheckHealth(context.Background(), backend.PluginContext{PluginID: testPluginID})
			require.Equal(t, ErrPluginNotRegistered, err)

			req, err := http.NewRequest(http.MethodGet, "/test", nil)
			require.NoError(t, err)
			w := httptest.NewRecorder()
			err = ctx.manager.callResourceInternal(w, req, backend.PluginContext{PluginID: testPluginID})
			require.Equal(t, ErrPluginNotRegistered, err)
		})
	})

	newManagerScenario(t, true, func(t *testing.T, ctx *managerScenarioCtx) {
		t.Run("Managed plugin scenario", func(t *testing.T) {
			ctx.license.edition = "Open Source"
			ctx.license.hasLicense = false
			ctx.cfg.BuildVersion = "7.0.0"

			t.Run("Should be able to register plugin", func(t *testing.T) {
				err := ctx.manager.Register(testPluginID, ctx.factory)
				require.NoError(t, err)
				require.NotNil(t, ctx.plugin)
				require.Equal(t, testPluginID, ctx.plugin.pluginID)
				require.NotNil(t, ctx.plugin.logger)

				t.Run("Should not be able to register an already registered plugin", func(t *testing.T) {
					err := ctx.manager.Register(testPluginID, ctx.factory)
					require.Error(t, err)
				})

				t.Run("Should provide expected host environment variables", func(t *testing.T) {
					require.Len(t, ctx.env, 2)
					require.EqualValues(t, []string{"GF_VERSION=7.0.0", "GF_EDITION=Open Source"}, ctx.env)
				})

				t.Run("When manager runs should start and stop plugin", func(t *testing.T) {
					pCtx := context.Background()
					cCtx, cancel := context.WithCancel(pCtx)
					var wg sync.WaitGroup
					wg.Add(1)
					go func() {
						ctx.manager.Run(cCtx)
						wg.Done()
					}()
					go func() {
						cancel()
					}()
					wg.Wait()
					require.True(t, ctx.plugin.started)
					require.True(t, ctx.plugin.stopped)
				})

				t.Run("Shouldn't be able to start managed plugin", func(t *testing.T) {
					err := ctx.manager.StartPlugin(context.Background(), testPluginID)
					require.NotNil(t, err)
				})

				t.Run("Unimplemented handlers", func(t *testing.T) {
					t.Run("Collect metrics should return method not implemented error", func(t *testing.T) {
						_, err = ctx.manager.CollectMetrics(context.Background(), testPluginID)
						require.Equal(t, ErrMethodNotImplemented, err)
					})

					t.Run("Check health should return method not implemented error", func(t *testing.T) {
						_, err = ctx.manager.CheckHealth(context.Background(), backend.PluginContext{PluginID: testPluginID})
						require.Equal(t, ErrMethodNotImplemented, err)
					})

					t.Run("Call resource should return method not implemented error", func(t *testing.T) {
						req, err := http.NewRequest(http.MethodGet, "/test", bytes.NewReader([]byte{}))
						require.NoError(t, err)
						w := httptest.NewRecorder()
						err = ctx.manager.callResourceInternal(w, req, backend.PluginContext{PluginID: testPluginID})
						require.Equal(t, ErrMethodNotImplemented, err)
					})
				})

				t.Run("Implemented handlers", func(t *testing.T) {
					t.Run("Collect metrics should return expected result", func(t *testing.T) {
						ctx.plugin.CollectMetricsHandlerFunc = backend.CollectMetricsHandlerFunc(func(ctx context.Context) (*backend.CollectMetricsResult, error) {
							return &backend.CollectMetricsResult{
								PrometheusMetrics: []byte("hello"),
							}, nil
						})

						res, err := ctx.manager.CollectMetrics(context.Background(), testPluginID)
						require.NoError(t, err)
						require.NotNil(t, res)
						require.Equal(t, "hello", string(res.PrometheusMetrics))
					})

					t.Run("Check health should return expected result", func(t *testing.T) {
						json := []byte(`{
							"key": "value"
						}`)
						ctx.plugin.CheckHealthHandlerFunc = backend.CheckHealthHandlerFunc(func(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
							return &backend.CheckHealthResult{
								Status:      backend.HealthStatusOk,
								Message:     "All good",
								JSONDetails: json,
							}, nil
						})

						res, err := ctx.manager.CheckHealth(context.Background(), backend.PluginContext{PluginID: testPluginID})
						require.NoError(t, err)
						require.NotNil(t, res)
						require.Equal(t, backend.HealthStatusOk, res.Status)
						require.Equal(t, "All good", res.Message)
						require.Equal(t, json, res.JSONDetails)
					})

					t.Run("Call resource should return expected response", func(t *testing.T) {
						ctx.plugin.CallResourceHandlerFunc = backend.CallResourceHandlerFunc(func(ctx context.Context, req *backend.CallResourceRequest, sender backend.CallResourceResponseSender) error {
							sender.Send(&backend.CallResourceResponse{
								Status: http.StatusOK,
							})
							return nil
						})

						req, err := http.NewRequest(http.MethodGet, "/test", bytes.NewReader([]byte{}))
						require.NoError(t, err)
						w := httptest.NewRecorder()
						err = ctx.manager.callResourceInternal(w, req, backend.PluginContext{PluginID: testPluginID})
						require.NoError(t, err)
						require.Equal(t, http.StatusOK, w.Code)
					})
				})
			})
		})
	})

	newManagerScenario(t, false, func(t *testing.T, ctx *managerScenarioCtx) {
		t.Run("Unmanaged plugin scenario", func(t *testing.T) {
			ctx.license.edition = "Open Source"
			ctx.license.hasLicense = false
			ctx.cfg.BuildVersion = "7.0.0"

			t.Run("Should be able to register plugin", func(t *testing.T) {
				err := ctx.manager.Register(testPluginID, ctx.factory)
				require.NoError(t, err)
				require.False(t, ctx.plugin.managed)

				t.Run("When manager runs should not start plugin", func(t *testing.T) {
					pCtx := context.Background()
					cCtx, cancel := context.WithCancel(pCtx)
					var wg sync.WaitGroup
					wg.Add(1)
					go func() {
						ctx.manager.Run(cCtx)
						wg.Done()
					}()
					go func() {
						cancel()
					}()
					wg.Wait()
					require.True(t, ctx.plugin.stopped)
				})

				t.Run("Should be able to start unmanaged plugin", func(t *testing.T) {
					err := ctx.manager.StartPlugin(context.Background(), testPluginID)
					require.Nil(t, err)
					require.True(t, ctx.plugin.started)
				})
			})
		})
	})

	newManagerScenario(t, true, func(t *testing.T, ctx *managerScenarioCtx) {
		t.Run("Plugin registration scenario when Grafana is licensed", func(t *testing.T) {
			ctx.license.edition = "Enterprise"
			ctx.license.hasLicense = true
			ctx.cfg.BuildVersion = "7.0.0"
			ctx.cfg.EnterpriseLicensePath = "/license.txt"

			err := ctx.manager.Register(testPluginID, ctx.factory)
			require.NoError(t, err)

			t.Run("Should provide expected host environment variables", func(t *testing.T) {
				require.Len(t, ctx.env, 3)
				require.EqualValues(t, []string{"GF_VERSION=7.0.0", "GF_EDITION=Enterprise", "GF_ENTERPRISE_LICENSE_PATH=/license.txt"}, ctx.env)
			})
		})
	})
}

type managerScenarioCtx struct {
	cfg     *setting.Cfg
	license *testLicensingService
	manager *manager
	factory PluginFactoryFunc
	plugin  *testPlugin
	env     []string
}

func newManagerScenario(t *testing.T, managed bool, fn func(t *testing.T, ctx *managerScenarioCtx)) {
	t.Helper()
	cfg := setting.NewCfg()
	license := &testLicensingService{}
	ctx := &managerScenarioCtx{
		cfg:     cfg,
		license: license,
		manager: &manager{
			Cfg:     cfg,
			License: license,
		},
	}

	err := ctx.manager.Init()
	require.NoError(t, err)

	ctx.factory = PluginFactoryFunc(func(pluginID string, logger log.Logger, env []string) (Plugin, error) {
		ctx.plugin = &testPlugin{
			pluginID: pluginID,
			logger:   logger,
			managed:  managed,
		}
		ctx.env = env

		return ctx.plugin, nil
	})

	fn(t, ctx)
}

type testPlugin struct {
	pluginID string
	logger   log.Logger
	started  bool
	stopped  bool
	managed  bool
	exited   bool
	backend.CollectMetricsHandlerFunc
	backend.CheckHealthHandlerFunc
	backend.CallResourceHandlerFunc
}

func (tp *testPlugin) PluginID() string {
	return tp.pluginID
}

func (tp *testPlugin) Logger() log.Logger {
	return tp.logger
}

func (tp *testPlugin) Start(ctx context.Context) error {
	tp.started = true
	return nil
}

func (tp *testPlugin) Stop(ctx context.Context) error {
	tp.stopped = true
	return nil
}

func (tp *testPlugin) IsManaged() bool {
	return tp.managed
}

func (tp *testPlugin) Exited() bool {
	return tp.exited
}

func (tp *testPlugin) CollectMetrics(ctx context.Context) (*backend.CollectMetricsResult, error) {
	if tp.CollectMetricsHandlerFunc != nil {
		return tp.CollectMetricsHandlerFunc(ctx)
	}

	return nil, ErrMethodNotImplemented
}

func (tp *testPlugin) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	if tp.CheckHealthHandlerFunc != nil {
		return tp.CheckHealthHandlerFunc(ctx, req)
	}

	return nil, ErrMethodNotImplemented
}

func (tp *testPlugin) CallResource(ctx context.Context, req *backend.CallResourceRequest, sender backend.CallResourceResponseSender) error {
	if tp.CallResourceHandlerFunc != nil {
		return tp.CallResourceHandlerFunc(ctx, req, sender)
	}

	return ErrMethodNotImplemented
}

type testLicensingService struct {
	edition    string
	hasLicense bool
}

func (t *testLicensingService) HasLicense() bool {
	return t.hasLicense
}

func (*testLicensingService) Expiry() int64 {
	return 0
}

func (t *testLicensingService) Edition() string {
	return t.edition
}

func (*testLicensingService) StateInfo() string {
	return ""
}

func (l *testLicensingService) LicenseURL(user *models.SignedInUser) string {
	return ""
}

func (*testLicensingService) HasValidLicense() bool {
	return false
}
