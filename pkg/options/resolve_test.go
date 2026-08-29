package options

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/options/resolver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"gotest.tools/assert"
)

type testCase struct {
	Name                       string
	ProviderOptions            map[string]*types.Option
	UserValues                 map[string]string
	ResolvedValues             map[string]config.OptionValue
	ResolvedDynamicDefinitions config.OptionDefinitions
	ExtraValues                map[string]string
	ResolveGlobal              bool
	DontResolveLocal           bool
	SkipRequired               bool

	ExpectErr              bool
	ExpectedOptions        map[string]string
	ExpectedDynamicOptions config.OptionDefinitions

	// FreshFilledKeys get Filled set to time.Now() right before Resolve runs.
	FreshFilledKeys []string
}

const (
	testOptTest    = "TEST"
	testValTest    = "test"
	testOptCommand = "COMMAND"
	testCmdEchoBar = "echo bar"
	testValBar     = "bar"
	testOptCmd1    = "COMMAND1"
	testOptCmd2    = "COMMAND2"
	testValFoo     = "foo"
	testOptExpire  = "EXPIRE"
	testOptNoExp   = "NOTEXPIRE"
	testOptParent  = "PARENT"
	testOptChild1  = "CHILD1"
	testOptChild2  = "CHILD2"
	testRefParent  = "${PARENT}"
	testOptTest2   = "TEST2"
	testValTest2   = "test2"
	testOptFoo     = "FOO"
	testValTest5   = "test5"
	testValTest3   = "test3"
	testValTest4   = "test4"
	testOptTest3   = "TEST3"
	testValTest1   = "test1"
	testOptTest4   = "TEST4"
	testRefChain34 = "${TEST3}-${FOO}-4"
	testRefChain24 = "${TEST2}-${FOO}-4"
	testOptTest5   = "TEST5"
)

var resolveOptionsTestCases = []testCase{
	{
		Name: "simple",
		ExtraValues: map[string]string{
			"WORKSPACE_ID": testValTest,
		},
		ProviderOptions: map[string]*types.Option{
			testOptTest: {
				Default: "${WORKSPACE_ID}-test",
			},
		},
		ExpectedOptions: map[string]string{
			testOptTest: "test-test",
		},
	},
	{
		Name: "dependency",
		ExtraValues: map[string]string{
			"WORKSPACE_ID": testValTest,
		},
		ProviderOptions: map[string]*types.Option{
			testOptTest: {
				Default: "${WORKSPACE_ID}-test-${COMMAND}-$COMMAND",
			},
			testOptCommand: {
				Command: testCmdEchoBar,
			},
		},
		ExpectedOptions: map[string]string{
			testOptTest:    "test-test-bar-bar",
			testOptCommand: testValBar,
		},
	},
	{
		Name: "No extra values",
		ProviderOptions: map[string]*types.Option{
			testOptCmd1: {
				Command: "echo ${COMMAND2}-test",
			},
			testOptCmd2: {
				Command: testCmdEchoBar,
			},
		},
		ExpectedOptions: map[string]string{
			testOptCmd1: "bar-test",
			testOptCmd2: testValBar,
		},
	},
	{
		Name: "Cyclic dep",
		ProviderOptions: map[string]*types.Option{
			testOptCmd1: {
				Command: "echo ${COMMAND2}",
			},
			testOptCmd2: {
				Command: "echo ${COMMAND1}",
			},
		},
		ExpectErr: true,
	},
	{
		Name: "Override",
		ResolvedValues: map[string]config.OptionValue{
			testOptCommand: {
				Value:        testValFoo,
				UserProvided: true,
			},
		},
		ProviderOptions: map[string]*types.Option{
			testOptCommand: {
				Command: testCmdEchoBar,
			},
		},
		ExpectedOptions: map[string]string{
			testOptCommand: testValFoo,
		},
	},
	{
		Name: "Override",
		ResolvedValues: map[string]config.OptionValue{
			testOptCommand: {
				Value:        testValFoo,
				UserProvided: true,
			},
		},
		ProviderOptions: map[string]*types.Option{
			testOptCommand: {
				Command: testCmdEchoBar,
			},
			testOptCmd1: {
				Command: "echo ${COMMAND}-foo-${UNDEFINED}",
			},
			"DEFAULT1": {
				Default: "${COMMAND}-foo-${UNDEFINED}",
			},
		},
		ExpectedOptions: map[string]string{
			testOptCommand: testValFoo,
			testOptCmd1:    "foo-foo-",
			"DEFAULT1":     "foo-foo-${UNDEFINED}",
		},
	},
	{
		Name: "Expire",
		ResolvedValues: map[string]config.OptionValue{
			testOptExpire: {
				Value:  testValFoo,
				Filled: &[]types.Time{types.NewTime(time.Time{})}[0],
			},
			testOptNoExp: {
				Value: testValFoo,
			},
		},
		ProviderOptions: map[string]*types.Option{
			testOptExpire: {
				Command: testCmdEchoBar,
				Cache:   "10m",
			},
			testOptNoExp: {
				Command: testCmdEchoBar,
				Cache:   "10m",
			},
		},
		ExpectedOptions: map[string]string{
			testOptExpire: testValBar,
			testOptNoExp:  testValFoo,
		},
		FreshFilledKeys: []string{testOptNoExp},
	},
	{
		Name: "Ignore self",
		ProviderOptions: map[string]*types.Option{
			"SELF": {
				Command: "SELF=test; echo ${SELF}",
			},
		},
		ExpectedOptions: map[string]string{
			"SELF": testValTest,
		},
	},
	{
		Name: "Recompute children",
		UserValues: map[string]string{
			testOptParent: testValFoo,
		},
		ResolvedValues: map[string]config.OptionValue{
			testOptParent: {
				Value:        testValTest,
				UserProvided: true,
			},
			testOptChild1: {
				Value: "test-child1",
			},
			testOptChild2: {
				Value: "test-child2",
			},
		},
		ProviderOptions: map[string]*types.Option{
			testOptParent: {},
			testOptChild1: {
				Command: "echo ${PARENT}-child1",
			},
			testOptChild2: {
				Default: "${PARENT}-child2",
			},
		},
		ExpectedOptions: map[string]string{
			testOptParent: testValFoo,
			testOptChild1: "foo-child1",
			testOptChild2: "foo-child2",
		},
	},
	{
		Name: "Error local global",
		ProviderOptions: map[string]*types.Option{
			testOptParent: {
				Default: testValTest,
			},
			testOptChild1: {
				Global:  true,
				Default: testRefParent,
			},
		},
		ExpectErr: true,
	},
	{
		Name: "Error local var",
		ProviderOptions: map[string]*types.Option{
			testOptParent: {
				Local:   true,
				Default: testValTest,
			},
			testOptChild1: {
				Default: testRefParent,
			},
		},
		ExpectErr: true,
	},
	{
		Name: "Don't resolve local",
		ProviderOptions: map[string]*types.Option{
			testOptParent: {
				Default: testValTest,
			},
			testOptChild1: {
				Default: testRefParent,
				Local:   true,
			},
		},
		DontResolveLocal: true,
		ExpectedOptions: map[string]string{
			testOptParent: testValTest,
		},
	},
	{
		Name: "Resolve",
		ProviderOptions: map[string]*types.Option{
			testOptParent: {
				Default: testValTest,
			},
			testOptChild1: {
				Default: testRefParent,
			},
		},
		DontResolveLocal: true,
		ExpectedOptions: map[string]string{
			testOptParent: testValTest,
			testOptChild1: testValTest,
		},
	},
	{
		Name: "Skip Required",
		ProviderOptions: map[string]*types.Option{
			testOptParent: {
				Required: true,
			},
			testOptChild1: {
				Default: testRefParent,
			},
			"PARENT2": {
				Required: true,
				Default:  testValTest,
			},
			testOptChild2: {
				Default: "${PARENT2}",
			},
		},
		SkipRequired: true,
		ExpectedOptions: map[string]string{
			"PARENT2":     testValTest,
			testOptChild2: testValTest,
		},
	},
	{
		Name: "Nested dynamic options",
		ProviderOptions: map[string]*types.Option{
			testOptTest: {
				Default: testValTest,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest2: &types.Option{
						Default: testValTest2,
					},
				}),
			},
			testOptFoo: {Command: testCmdEchoBar},
		},
		ExpectedOptions: map[string]string{
			testOptTest:  testValTest,
			testOptTest2: testValTest2,
			testOptFoo:   testValBar,
		},
		ExpectedDynamicOptions: config.OptionDefinitions{
			testOptTest2: &types.Option{
				Default: testValTest2,
			},
		},
	},
	{
		Name: "Dynamic options don't update",
		ProviderOptions: map[string]*types.Option{
			testOptTest: {
				Default: testValTest,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest2: &types.Option{
						Default: testValTest2,
					},
				}),
			},
			testOptFoo: {Command: testCmdEchoBar},
		},
		ResolvedDynamicDefinitions: map[string]*types.Option{
			testOptTest2: {
				Default: testValTest5,
			},
		},
		ResolvedValues: map[string]config.OptionValue{
			testOptTest: {
				Value:        testValTest3,
				Children:     []string{testOptTest2},
				UserProvided: true,
			},
			testOptTest2: {Value: testValTest4, UserProvided: true},
		},
		ExpectedOptions: map[string]string{
			testOptTest:  testValTest3,
			testOptTest2: testValTest4,
			testOptFoo:   testValBar,
		},
		ExpectedDynamicOptions: config.OptionDefinitions{
			testOptTest2: {
				Default: testValTest2,
			},
		},
	},
	{
		Name: "Dynamic options update",
		ProviderOptions: map[string]*types.Option{
			testOptTest: {
				Default: testValTest,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest3: &types.Option{
						Default: testValTest2,
					},
				}),
			},
			testOptFoo: {Command: testCmdEchoBar},
		},
		UserValues: map[string]string{
			testOptTest: testValTest1,
		},
		ResolvedValues: map[string]config.OptionValue{
			testOptTest:  {Value: testValTest3, Children: []string{testOptTest2}},
			testOptTest2: {Value: testValTest4},
		},
		ResolvedDynamicDefinitions: map[string]*types.Option{
			testOptTest2: {
				Default: testValTest5,
			},
		},
		ExpectedOptions: map[string]string{
			testOptTest:  testValTest1,
			testOptTest3: testValTest2,
			testOptFoo:   testValBar,
		},
		ExpectedDynamicOptions: config.OptionDefinitions{
			testOptTest3: &types.Option{
				Default: testValTest2,
			},
		},
	},
	{
		Name: "Nested dynamic options",
		ProviderOptions: map[string]*types.Option{
			testOptTest: {
				Default: testValTest1,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest2: &types.Option{
						Default: testValTest2,
						SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
							testOptTest3: &types.Option{
								Default: testValTest3,
								SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
									testOptTest4: &types.Option{
										Default: testRefChain34,
									},
								}),
							},
						}),
					},
				}),
			},
			testOptFoo: {Command: testCmdEchoBar},
		},
		ExpectedOptions: map[string]string{
			testOptTest:  testValTest1,
			testOptTest2: testValTest2,
			testOptTest3: testValTest3,
			testOptTest4: "test3-bar-4",
			testOptFoo:   testValBar,
		},
		ExpectedDynamicOptions: config.OptionDefinitions{
			testOptTest2: &types.Option{
				Default: testValTest2,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest3: &types.Option{
						Default: testValTest3,
						SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
							testOptTest4: &types.Option{
								Default: testRefChain34,
							},
						}),
					},
				}),
			},
			testOptTest3: &types.Option{
				Default: testValTest3,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest4: &types.Option{
						Default: testRefChain34,
					},
				}),
			},
			testOptTest4: &types.Option{
				Default: testRefChain34,
			},
		},
	},
	{
		Name: "Nested dynamic options skip required",
		ProviderOptions: map[string]*types.Option{
			testOptTest: {
				Default: testValTest1,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest2: &types.Option{
						Required: true,
						SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
							testOptTest3: &types.Option{
								Default: testValTest3,
								SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
									testOptTest4: &types.Option{
										Default: testRefChain34,
									},
								}),
							},
						}),
					},
				}),
			},
			testOptFoo: {Command: testCmdEchoBar},
		},
		SkipRequired: true,
		ExpectedOptions: map[string]string{
			testOptTest: testValTest1,
			testOptFoo:  testValBar,
		},
		ExpectedDynamicOptions: config.OptionDefinitions{
			testOptTest2: &types.Option{
				Required: true,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest3: &types.Option{
						Default: testValTest3,
						SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
							testOptTest4: &types.Option{
								Default: testRefChain34,
							},
						}),
					},
				}),
			},
		},
	},
	{
		Name: "Nested dynamic options use option",
		ProviderOptions: map[string]*types.Option{
			testOptTest: {
				Default: testValTest1,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest2: &types.Option{
						Required: true,
						SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
							testOptTest3: &types.Option{
								Default: testValTest3,
								SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
									testOptTest4: &types.Option{
										Default: testRefChain24,
									},
								}),
							},
						}),
					},
				}),
			},
			testOptFoo: {Command: testCmdEchoBar},
		},
		SkipRequired: true,
		UserValues: map[string]string{
			testOptTest2: testValTest2,
		},
		ExpectedOptions: map[string]string{
			testOptTest:  testValTest1,
			testOptTest2: testValTest2,
			testOptTest3: testValTest3,
			testOptTest4: "test2-bar-4",
			testOptFoo:   testValBar,
		},
		ExpectedDynamicOptions: config.OptionDefinitions{
			testOptTest2: &types.Option{
				Required: true,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest3: &types.Option{
						Default: testValTest3,
						SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
							testOptTest4: &types.Option{
								Default: testRefChain24,
							},
						}),
					},
				}),
			},
			testOptTest3: &types.Option{
				Default: testValTest3,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest4: &types.Option{
						Default: testRefChain24,
					},
				}),
			},
			testOptTest4: &types.Option{
				Default: testRefChain24,
			},
		},
	},
	{
		Name: "Nested dynamic options use option",
		ProviderOptions: map[string]*types.Option{
			testOptTest: {
				Default: testValTest1,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest2: &types.Option{
						Default: testValTest2,
						SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
							testOptTest3: &types.Option{
								Default: testValTest3,
							},
						}),
					},
				}),
			},
			testOptFoo: {Command: testCmdEchoBar},
		},
		ResolvedValues: map[string]config.OptionValue{
			testOptTest5: {
				Value: testValTest5,
			},
		},
		ExpectedOptions: map[string]string{
			testOptTest:  testValTest1,
			testOptTest2: testValTest2,
			testOptTest3: testValTest3,
			testOptFoo:   testValBar,
		},
		ExpectedDynamicOptions: config.OptionDefinitions{
			testOptTest2: &types.Option{
				Default: testValTest2,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest3: &types.Option{
						Default: testValTest3,
					},
				}),
			},
			testOptTest3: &types.Option{
				Default: testValTest3,
			},
		},
	},
	{
		Name: "Dynamic options unused option",
		ProviderOptions: map[string]*types.Option{
			testOptTest: {
				Default: testValTest1,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest2: &types.Option{
						Default: testValTest2,
					},
				}),
			},
			testOptFoo: {Command: testCmdEchoBar},
		},
		ResolvedValues: map[string]config.OptionValue{
			testOptTest5: {
				Value: testValTest5,
			},
		},
		ResolvedDynamicDefinitions: map[string]*types.Option{
			testOptTest5: {
				Default: testValTest2,
			},
		},
		ExpectedOptions: map[string]string{
			testOptTest:  testValTest1,
			testOptTest2: testValTest2,
			testOptFoo:   testValBar,
		},
		ExpectedDynamicOptions: config.OptionDefinitions{
			testOptTest2: &types.Option{
				Default: testValTest2,
			},
		},
	},
	{
		Name: "Dynamic options update default",
		ProviderOptions: map[string]*types.Option{
			testOptTest: {
				Default: testValTest1,
				SubOptionsCommand: optionsToSubCommand(config.OptionDefinitions{
					testOptTest2: &types.Option{
						Default: testValTest3,
					},
				}),
			},
			testOptFoo: {Command: testCmdEchoBar},
		},
		ResolvedValues: map[string]config.OptionValue{
			testOptTest: {
				Value: testValTest1,
			},
			testOptTest2: {
				Value: testValTest2,
			},
		},
		ResolvedDynamicDefinitions: map[string]*types.Option{
			testOptTest2: {
				Default: testValTest2,
			},
		},
		ExpectedOptions: map[string]string{
			testOptTest:  testValTest1,
			testOptTest2: testValTest3,
			testOptFoo:   testValBar,
		},
		ExpectedDynamicOptions: config.OptionDefinitions{
			testOptTest2: &types.Option{
				Default: testValTest3,
			},
		},
	},
}

func TestResolveOptions(t *testing.T) {
	for _, tc := range resolveOptionsTestCases {
		t.Run(tc.Name, func(t *testing.T) {
			runResolveTestCase(t, tc)
		})
	}
}

func applyFreshFilledTimestamps(tc testCase) {
	for _, key := range tc.FreshFilledKeys {
		value := tc.ResolvedValues[key]
		now := types.Now()
		value.Filled = &now
		tc.ResolvedValues[key] = value
	}
}

func runResolveTestCase(t *testing.T, tc testCase) {
	t.Helper()
	applyFreshFilledTimestamps(tc)
	r := resolver.New(tc.UserValues, tc.ExtraValues, buildResolverOpts(tc)...)
	options, dynamicOptions, err := r.Resolve(
		context.Background(),
		tc.ResolvedDynamicDefinitions,
		tc.ProviderOptions,
		tc.ResolvedValues,
	)
	if tc.ExpectErr {
		if err == nil {
			t.Fatalf("expected error, got nil error in test case %s", tc.Name)
		}

		return
	}

	assert.NilError(t, err, tc.Name)
	assertResolvedOptions(t, tc, options, dynamicOptions)
}

func buildResolverOpts(tc testCase) []resolver.Option {
	resolverOpts := []resolver.Option{
		resolver.WithSkipRequired(tc.SkipRequired),
		resolver.WithResolveSubOptions(),
	}
	if !tc.DontResolveLocal {
		resolverOpts = append(resolverOpts, resolver.WithResolveLocal())
	}
	if tc.ResolveGlobal {
		resolverOpts = append(resolverOpts, resolver.WithResolveGlobal())
	}

	return resolverOpts
}

func assertResolvedOptions(
	t *testing.T,
	tc testCase,
	options map[string]config.OptionValue,
	dynamicOptions config.OptionDefinitions,
) {
	t.Helper()
	strOptions := map[string]string{}
	for k, v := range options {
		strOptions[k] = v.Value
	}
	if len(tc.ExpectedOptions) > 0 {
		assert.DeepEqual(t, strOptions, tc.ExpectedOptions)
	} else {
		assert.DeepEqual(t, strOptions, map[string]string{})
	}

	if len(tc.ExpectedDynamicOptions) > 0 {
		assert.DeepEqual(t, dynamicOptions, tc.ExpectedDynamicOptions)
	} else {
		assert.DeepEqual(t, dynamicOptions, config.OptionDefinitions{})
	}
}

func optionsToSubCommand(optionDefinitions config.OptionDefinitions) string {
	out, _ := json.Marshal(&provider.SubOptions{
		Options: optionDefinitions,
	})
	return fmt.Sprintf("echo %q | base64 --decode", base64.StdEncoding.EncodeToString(out))
}

// TestResolveAgentAppleConfig locks in the fix for ${CONTAINER_PATH}-style
// option substitution into the apple driver config, which previously reached
// exec unresolved because no per-driver resolver existed for apple.
func TestResolveAgentAppleConfig(t *testing.T) {
	agentConfig := &provider.ProviderAgentConfig{}
	agentConfig.Apple.Path = "${CONTAINER_PATH}"
	agentConfig.Apple.Rosetta = types.StrBool("${ROSETTA}")
	agentConfig.Apple.Env = map[string]string{"KEY": "${VAL}"}

	options := map[string]string{
		"CONTAINER_PATH": "/opt/homebrew/bin/container",
		"ROSETTA":        "true",
		"VAL":            "resolved",
	}

	resolveAgentAppleConfig(agentConfig, options)

	assert.Equal(t, "/opt/homebrew/bin/container", agentConfig.Apple.Path)
	assert.Equal(t, types.StrBool("true"), agentConfig.Apple.Rosetta)
	assert.Equal(t, "resolved", agentConfig.Apple.Env["KEY"])
}

func TestResolveAgentMicrosandboxConfig(t *testing.T) {
	agentConfig := &provider.ProviderAgentConfig{}
	agentConfig.Microsandbox.Memory = "${MICROSANDBOX_MEMORY}"
	agentConfig.Microsandbox.CPUs = "${MICROSANDBOX_CPUS}"
	agentConfig.Microsandbox.Ephemeral = types.StrBool("${MICROSANDBOX_EPHEMERAL}")

	agentConfig.Microsandbox.MaxMemory = "${MICROSANDBOX_MAX_MEMORY}"
	agentConfig.Microsandbox.BlockEgress = types.StrBool("${MICROSANDBOX_BLOCK_EGRESS}")
	agentConfig.Microsandbox.Storage = "${MICROSANDBOX_STORAGE}"

	options := map[string]string{
		"MICROSANDBOX_MEMORY":       "2048",
		"MICROSANDBOX_CPUS":         "4",
		"MICROSANDBOX_EPHEMERAL":    "true",
		"MICROSANDBOX_MAX_MEMORY":   "8192",
		"MICROSANDBOX_BLOCK_EGRESS": "true",
		"MICROSANDBOX_STORAGE":      "32",
	}

	resolveAgentMicrosandboxConfig(agentConfig, options)

	assert.Equal(t, "2048", agentConfig.Microsandbox.Memory)
	assert.Equal(t, "4", agentConfig.Microsandbox.CPUs)
	assert.Equal(t, types.StrBool("true"), agentConfig.Microsandbox.Ephemeral)
	assert.Equal(t, "8192", agentConfig.Microsandbox.MaxMemory)
	assert.Equal(t, types.StrBool("true"), agentConfig.Microsandbox.BlockEgress)
	assert.Equal(t, "32", agentConfig.Microsandbox.Storage)
}

func TestResolveAgentDownloadURL(t *testing.T) {
	const (
		localHost         = "http://localhost:8080/"
		localHostTrail    = "http://localhost:8080///"
		exampleAgent      = "https://example.com/agent/"
		exampleAgentTrail = "https://example.com/agent///"
		exampleAgentPlain = "https://example.com/agent"
		defaultURL        = config.AgentLatestDownloadURL
	)

	cases := []struct {
		name       string
		envURL     string
		contextURL string
		want       string
	}{
		{
			name:       "env override wins over context option",
			envURL:     localHost,
			contextURL: exampleAgentPlain,
			want:       localHost,
		},
		{
			name:   "env value trailing slash normalized to single",
			envURL: localHostTrail,
			want:   localHost,
		},
		{name: "context option used when env unset", contextURL: exampleAgent, want: exampleAgent},
		{
			name:       "context option trailing slash normalized",
			contextURL: exampleAgentTrail,
			want:       exampleAgent,
		},
		{name: "default release url when neither set", want: defaultURL},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvAgentURL, tc.envURL)

			devConfig := &config.Config{
				DefaultContext: config.DefaultContext,
				Contexts: map[string]*config.ContextConfig{
					config.DefaultContext: {},
				},
			}
			if tc.contextURL != "" {
				devConfig.Contexts[config.DefaultContext].Options = map[string]config.OptionValue{
					config.ContextOptionAgentURL: {Value: tc.contextURL},
				}
			}

			assert.Equal(t, tc.want, resolveAgentDownloadURL(devConfig))
		})
	}
}

func TestResolveAgentKubernetesConfigAgentSecurityContext(t *testing.T) {
	agentConfig := &provider.ProviderAgentConfig{}
	options := map[string]string{
		"AGENT_SECURITY_CONTEXT": "runAsUser: 1000",
	}
	agentConfig.Kubernetes.AgentSecurityContext = "${AGENT_SECURITY_CONTEXT}"

	resolveAgentKubernetesConfig(agentConfig, options)

	if got := agentConfig.Kubernetes.AgentSecurityContext; got != "runAsUser: 1000" {
		t.Errorf("AgentSecurityContext = %q, want %q", got, "runAsUser: 1000")
	}
}

func TestResolveAgentKubernetesConfigAgentInstallPath(t *testing.T) {
	agentConfig := &provider.ProviderAgentConfig{}
	options := map[string]string{
		"AGENT_INSTALL_PATH": "/home/vscode/.local/bin/devsy",
	}
	agentConfig.Kubernetes.AgentInstallPath = "${AGENT_INSTALL_PATH}"

	resolveAgentKubernetesConfig(agentConfig, options)

	if got := agentConfig.Kubernetes.AgentInstallPath; got != "/home/vscode/.local/bin/devsy" {
		t.Errorf("AgentInstallPath = %q, want %q", got, "/home/vscode/.local/bin/devsy")
	}
}
