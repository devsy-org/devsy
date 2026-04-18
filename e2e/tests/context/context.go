package context

import (
	"context"
	"encoding/json"
	"os"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const ideIntelliJ = "intellij"

var _ = ginkgo.Describe(
	"devsy context test suite",
	ginkgo.Label("context"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeAll(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"create a new context, switch to it and delete afterwards",
			ginkgo.SpecTimeout(framework.GetTimeout()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				var err error
				err = f.DevsyContextCreate(ctx, "test-context")
				framework.ExpectNoError(err)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					cleanupErr := f.DevsyContextDelete(cleanupCtx, "test-context")
					framework.ExpectNoError(cleanupErr)
				})

				err = f.DevsyContextUse(ctx, "test-context")
				framework.ExpectNoError(err)
			},
		)

		ginkgo.It(
			"should use shared context in IDE commands",
			ginkgo.SpecTimeout(framework.GetTimeout()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				contextA := "test-ctx-a-ide"
				contextB := "test-ctx-b-ide"

				var err error
				err = f.DevsyContextCreate(ctx, contextA)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyContextDelete(cleanupCtx, contextA)
				})

				err = f.DevsyContextCreate(ctx, contextB)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					err = f.DevsyContextDelete(cleanupCtx, contextB)
					framework.ExpectNoError(err)
				})

				err = f.DevsyContextUse(ctx, contextA)
				framework.ExpectNoError(err)

				err = f.DevsyIDEUse(ctx, ideIntelliJ, "--context", contextB)
				framework.ExpectNoError(err)

				output, err := f.DevsyIDEList(ctx, "--output", "json")
				framework.ExpectNoError(err)

				var ides []map[string]any
				err = json.Unmarshal([]byte(output), &ides)
				framework.ExpectNoError(err)
				gomega.Expect(ides).NotTo(gomega.BeEmpty(), "IDE list should not be empty")

				for _, ide := range ides {
					if ide["name"] == ideIntelliJ {
						if defaultVal, exists := ide["default"]; exists && defaultVal == true {
							ginkgo.Fail("IDE was incorrectly set in context-a instead of context-b")
						}
					}
				}

				output, err = f.DevsyIDEList(ctx, "--context", contextB, "--output", "json")
				framework.ExpectNoError(err)

				err = json.Unmarshal([]byte(output), &ides)
				framework.ExpectNoError(err)
				gomega.Expect(ides).
					NotTo(gomega.BeEmpty(), "IDE list for context-b should not be empty")

				intellijFound := false
				for _, ide := range ides {
					if ide["name"] == ideIntelliJ {
						if defaultVal, exists := ide["default"]; exists && defaultVal == true {
							intellijFound = true
							break
						}
					}
				}
				gomega.Expect(intellijFound).To(
					gomega.BeTrue(), "IDE should be set as default in context-b",
				)

				ginkgo.GinkgoT().Setenv("DEVSY_CONTEXT", contextB)

				output, err = f.DevsyIDEList(ctx, "--output", "json")
				framework.ExpectNoError(err)

				err = json.Unmarshal([]byte(output), &ides)
				framework.ExpectNoError(err)
				gomega.Expect(ides).NotTo(
					gomega.BeEmpty(),
					"IDE list via DEVSY_CONTEXT should not be empty",
				)

				intellijFound = false
				for _, ide := range ides {
					if ide["name"] == ideIntelliJ {
						if defaultVal, exists := ide["default"]; exists && defaultVal == true {
							intellijFound = true
							break
						}
					}
				}
				gomega.Expect(intellijFound).To(
					gomega.BeTrue(),
					"DEVSY_CONTEXT env var should select context-b with intellij as default IDE",
				)
			},
		)
	},
)
