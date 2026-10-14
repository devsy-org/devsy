// Package specreport validates a Ginkgo JSON report against an Expectation.
//
// A green exit status alone does not prove the intended specs ran, so Validate
// requires the labelled specs to have executed, and the required titles to
// have each executed exactly once and passed.
package specreport
