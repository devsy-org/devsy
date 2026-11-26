package provider

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

const (
	pool          = "a"
	id            = "p-a-xxxxxxxxxx"
	networkKind   = "network"
	missingReason = "session_label_missing"
	invalidReason = "session_label_invalid"
)

type ClassifySuite struct{ suite.Suite }

func TestClassifySuite(t *testing.T) { suite.Run(t, new(ClassifySuite)) }

func (s *ClassifySuite) TestLabelClassification() {
	for _, tt := range []struct {
		name   string
		labels map[string]string
		owned  bool
		reason string
	}{
		{name: "owned", labels: map[string]string{LabelPool: pool, LabelSession: id}, owned: true},
		{name: "missing session label", labels: map[string]string{LabelPool: pool}, reason: missingReason},
		{name: "empty session label", labels: map[string]string{LabelPool: pool, LabelSession: ""}, reason: invalidReason},
		{name: "malformed ID", labels: map[string]string{LabelPool: pool, LabelSession: "X"}, reason: invalidReason},
		{
			name: "different pool ID", labels: map[string]string{LabelPool: pool, LabelSession: "p-b-xxxxxxxxxx"},
			reason: invalidReason,
		},
		{
			name: "pool prefix only", labels: map[string]string{LabelPool: pool, LabelSession: "p-a-b-xxxxxxxxxx"},
			reason: invalidReason,
		},
		{name: "other pool", labels: map[string]string{LabelPool: "a-b", LabelSession: id}},
		{name: "different case", labels: map[string]string{LabelPool: "A", LabelSession: id}},
		{name: "missing pool label", labels: map[string]string{LabelSession: id}},
		{name: "no labels"},
	} {
		s.Run(tt.name, func() {
			r := Resource{Kind: networkKind, Name: "example", UID: "object-1", Labels: tt.labels}
			owned, orphans, err := Classify(pool, []Resource{r})
			s.Require().NoError(err)
			wantOwned := map[string][]Resource{}
			if tt.owned {
				wantOwned[id] = []Resource{r}
			}
			s.Equal(wantOwned, owned)
			var wantOrphans []Orphan
			if tt.reason != "" {
				wantOrphans = []Orphan{{Resource: r, Reason: tt.reason}}
			}
			s.Equal(wantOrphans, orphans)
		})
	}
}

func (s *ClassifySuite) TestRejectsInvalidPoolNames() {
	for _, name := range []string{"", "A", "a_b", strings.Repeat("a", 33)} {
		s.Run("pool="+name, func() {
			input := []Resource{
				{Kind: networkKind, Name: "example", Labels: map[string]string{LabelPool: name}},
				{Kind: networkKind, Name: "unlabelled"},
			}
			owned, orphans, err := Classify(name, input)
			s.Require().ErrorIs(err, ErrInvalidPool)
			s.Empty(owned)
			s.Empty(orphans)
		})
	}
}

func (s *ClassifySuite) TestSortedCopiesAcrossInputOrders() {
	const secondID = "p-a-yyyyyyyyyy"
	ownedLabels := map[string]string{LabelPool: pool, LabelSession: id}
	orphanLabels := map[string]string{LabelPool: pool}
	input := []Resource{
		{Kind: "workspace", Name: "a", Labels: ownedLabels},
		{Kind: "workspace", Name: "a", Labels: orphanLabels},
		{Kind: networkKind, Name: "z", Labels: ownedLabels},
		{Kind: networkKind, Name: "z", Labels: orphanLabels},
		{Kind: networkKind, Name: "b", Labels: ownedLabels},
		{Kind: networkKind, Name: "b", Labels: orphanLabels},
		{
			Kind:   networkKind,
			Name:   "other",
			Labels: map[string]string{LabelPool: pool, LabelSession: secondID},
		},
	}
	before := slices.Clone(input)
	for i := range before {
		before[i].Labels = maps.Clone(before[i].Labels)
	}
	owned, orphans, err := Classify(pool, input)
	s.Require().NoError(err)
	s.Equal(map[string][]Resource{
		id:       {input[4], input[2], input[0]},
		secondID: {input[6]},
	}, owned)
	s.Equal([]Orphan{
		{Resource: input[5], Reason: missingReason},
		{Resource: input[3], Reason: missingReason},
		{Resource: input[1], Reason: missingReason},
	}, orphans)

	reversed := slices.Clone(input)
	slices.Reverse(reversed)
	reversedOwned, reversedOrphans, err := Classify(pool, reversed)
	s.Require().NoError(err)
	s.Equal(owned, reversedOwned)
	s.Equal(orphans, reversedOrphans)

	owned[id][0].Labels[LabelSession] = "changed"
	orphans[0].Resource.Labels[LabelPool] = "changed"
	s.Equal(before, input)
	s.Equal(id, owned[id][1].Labels[LabelSession])
	s.Equal(pool, orphans[1].Resource.Labels[LabelPool])
}
