package counter2dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"slices"

	gd "github.com/perses/perses/go-sdk/dashboard"
	panel "github.com/perses/perses/go-sdk/panel"
	gp "github.com/perses/perses/go-sdk/panel-group"
	gq "github.com/perses/perses/go-sdk/query"
	ma "github.com/perses/perses/pkg/model/api/v1"
	sgq "github.com/perses/plugins/prometheus/sdk/go/query"
	tc "github.com/perses/plugins/timeserieschart/sdk/go"
	dto "github.com/prometheus/client_model/go"
	ce "github.com/prometheus/common/expfmt"
	cm "github.com/prometheus/common/model"
)

type FamilyToPanelOpt func(context.Context, *dto.MetricFamily) (gp.Option, error)

type PromQL string

func (q PromQL) ToOption() gq.Option { return sgq.PromQL(string(q)) }

func (q PromQL) ToPanelOpt() panel.Option {
	var opt gq.Option = q.ToOption()
	return panel.AddQuery(opt)
}

type MetnameToQuery func(context.Context, string) (PromQL, error)
type MetnameToTitle func(context.Context, string) (string, error)

type FamilyToPanelOptBuilder struct {
	MetnameToQuery
	MetnameToTitle
}

func (b FamilyToPanelOptBuilder) Build(copt panel.Option) FamilyToPanelOpt {
	return func(ctx context.Context, met *dto.MetricFamily) (gp.Option, error) {
		title, terr := b.MetnameToTitle(ctx, met.GetName())
		query, qerr := b.MetnameToQuery(ctx, met.GetName())
		return gp.AddPanel(
			title,
			copt,
			query.ToPanelOpt(),
		), errors.Join(terr, qerr)
	}
}

type DurationForRate struct{ raw string }

type DurationForRateValidator func(context.Context, string) (string, error)

func (v DurationForRateValidator) Convert(
	ctx context.Context,
	raw string,
) (DurationForRate, error) {
	validDuration, err := v(ctx, raw)
	return DurationForRate{raw: validDuration}, err
}

var (
	DurationForRate5m DurationForRate = DurationForRate{raw: "5m"}
)

type StaticFilter interface{ FilterString() string }

type SimpleStaticFilter string

//nolint:ireturn
func (s SimpleStaticFilter) AsStaticFilter() StaticFilter { return s }
func (s SimpleStaticFilter) FilterString() string         { return string(s) }

type FilterNop struct{}

func (n FilterNop) ToFilterString(_ string) string { return "" }

func (n FilterNop) FilterString() string { return "" }

type Rate struct {
	DurationForRate
	StaticFilter
}

var rate5m Rate = Rate{DurationForRate: DurationForRate5m}
var RateDefault Rate = rate5m.WithFilter(FilterNop{})

func (r Rate) WithFilter(filt StaticFilter) Rate {
	return Rate{
		DurationForRate: r.DurationForRate,
		StaticFilter:    filt,
	}
}

func (r Rate) ToMetnameToQuery() MetnameToQuery {
	return func(_ context.Context, metname string) (PromQL, error) {
		return PromQL(fmt.Sprintf(
			"rate(%s%s[%s])",
			metname,
			r.StaticFilter.FilterString(),
			r.DurationForRate.raw,
		)), nil
	}
}

func (r Rate) ToMetnameToTitle() MetnameToTitle {
	return func(_ context.Context, metname string) (string, error) {
		return "Rate " + metname, nil
	}
}

func (r Rate) ToFamilyToPaneloptBldr() FamilyToPanelOptBuilder {
	return FamilyToPanelOptBuilder{
		MetnameToQuery: r.ToMetnameToQuery(),
		MetnameToTitle: r.ToMetnameToTitle(),
	}
}

func (r Rate) ToFamilyToPanelOpt(copt panel.Option) FamilyToPanelOpt {
	return r.ToFamilyToPaneloptBldr().Build(copt)
}

func (r Rate) ToFamilyToPanelOptTSChart() FamilyToPanelOpt {
	return r.ToFamilyToPanelOpt(tc.Chart())
}

var FamilyToPanelOptDefault FamilyToPanelOpt = RateDefault.
	ToFamilyToPanelOptTSChart()

type MetSource func(context.Context) ([]*dto.MetricFamily, error)

type MetGroup struct {
	Name        string
	MetFamilies []*dto.MetricFamily
}

type Grouper func(context.Context, []*dto.MetricFamily) (MetGroups, error)

func (s MetSource) ToMetGroups(
	ctx context.Context,
	grouper Grouper,
) (MetGroups, error) {
	families, err := s(ctx)
	if nil != err {
		return nil, err
	}

	return grouper(ctx, families)
}

type GroupsSource func(context.Context) (MetGroups, error)

func (s MetSource) ToGroupsSource(grouper Grouper) GroupsSource {
	return func(ctx context.Context) (MetGroups, error) {
		return s.ToMetGroups(ctx, grouper)
	}
}

type SingleGroupName string

func (s SingleGroupName) ToGrouper() Grouper {
	return func(_ context.Context, arr []*dto.MetricFamily) (MetGroups, error) {
		return MetGroup{
			Name:        string(s),
			MetFamilies: arr,
		}.ToSingleGroup(), nil
	}
}

type BytesToMet func([]byte) ([]*dto.MetricFamily, error)

type PromParser struct{ ce.TextParser }

type PromParserBuilder struct{ cm.ValidationScheme }

var PromParserBuilderEmpty PromParserBuilder

func (b PromParserBuilder) Legacy() PromParserBuilder {
	return PromParserBuilder{ValidationScheme: cm.LegacyValidation}
}

func (b PromParserBuilder) UTF8() PromParserBuilder {
	return PromParserBuilder{ValidationScheme: cm.UTF8Validation}
}

func (b PromParserBuilder) ToParser() PromParser {
	return PromParser{TextParser: ce.NewTextParser(b.ValidationScheme)}
}

func (p *PromParser) ReaderToMetMap(
	rdr io.Reader,
) (map[string]*dto.MetricFamily, error) {
	return p.TextParser.TextToMetricFamilies(rdr)
}

func (p *PromParser) ToBytesToMet() BytesToMet {
	return func(raw []byte) ([]*dto.MetricFamily, error) {
		mfmap, err := p.ReaderToMetMap(bytes.NewReader(raw))
		if nil != err {
			return nil, err
		}
		var ival iter.Seq[*dto.MetricFamily] = maps.Values(mfmap)
		return slices.Collect(ival), nil
	}
}

type RawMetSource func(context.Context) ([]byte, error)

func (r RawMetSource) ToMetSource(conv BytesToMet) MetSource {
	return func(ctx context.Context) ([]*dto.MetricFamily, error) {
		raw, err := r(ctx)
		if nil != err {
			return nil, err
		}
		return conv(raw)
	}
}

func ReaderToRawMetSource(limit int64) func(io.Reader) RawMetSource {
	return func(rdr io.Reader) RawMetSource {
		return func(_ context.Context) ([]byte, error) {
			var buf bytes.Buffer
			lmtd := &io.LimitedReader{R: rdr, N: limit}
			_, err := io.Copy(&buf, lmtd)
			return buf.Bytes(), err
		}
	}
}

func (g MetGroup) ToOpts(
	ctx context.Context,
	conv FamilyToPanelOpt,
) ([]gp.Option, error) {
	var ret []gp.Option = make([]gp.Option, 0, len(g.MetFamilies))
	for _, met := range g.MetFamilies {
		var mtyp dto.MetricType = met.GetType()
		if dto.MetricType_COUNTER != mtyp {
			continue
		}

		opt, err := conv(ctx, met)
		if nil != err {
			return nil, err
		}

		ret = append(ret, opt)
	}
	return ret, nil
}

func (g MetGroup) ToOpt(
	ctx context.Context,
	conv FamilyToPanelOpt,
) (gd.Option, error) {
	panels, e := g.ToOpts(ctx, conv)
	if nil != e {
		return nil, e
	}
	return gd.AddPanelGroup(g.Name, panels...), nil
}

func (g MetGroup) ToSingleGroup() MetGroups { return []MetGroup{g} }

type DashboardName string

type ProjectName string

type MetGroups []MetGroup

func (g MetGroups) ToOpts(
	ctx context.Context,
	conv FamilyToPanelOpt,
) ([]gd.Option, error) {
	var opts []gd.Option = make([]gd.Option, 0, len(g))
	for _, mgrp := range g {
		opt, err := mgrp.ToOpt(ctx, conv)
		if nil != err {
			return nil, err
		}
		opts = append(opts, opt)
	}
	return opts, nil
}

type GroupsToDash func(context.Context, MetGroups) (ma.Dashboard, error)

type DashSource func(context.Context) (ma.Dashboard, error)

func (g GroupsSource) ToDashSource(conv GroupsToDash) DashSource {
	return func(ctx context.Context) (ma.Dashboard, error) {
		var empty ma.Dashboard

		grps, err := g(ctx)
		if nil != err {
			return empty, err
		}

		return conv(ctx, grps)
	}
}

type DashboardBuilder struct {
	DashboardName
	ProjectName
	FamilyToPanelOpt
}

func (d DashboardBuilder) ToBuilder(
	ctx context.Context,
	grps MetGroups,
) (gd.Builder, error) {
	var empty gd.Builder

	groups, err := grps.ToOpts(ctx, d.FamilyToPanelOpt)
	if nil != err {
		return empty, err
	}

	var opts []gd.Option = make([]gd.Option, 0, 1+len(groups))
	opts = append(opts, gd.ProjectName(string(d.ProjectName)))
	opts = append(opts, groups...)
	return gd.New(
		string(d.DashboardName),
		opts...,
	)
}

func (d DashboardBuilder) BuildDashboard(
	ctx context.Context,
	grps MetGroups,
) (ma.Dashboard, error) {
	var empty ma.Dashboard

	bldr, err := d.ToBuilder(ctx, grps)
	if nil != err {
		return empty, err
	}

	return bldr.Dashboard, nil
}

func (d DashboardBuilder) AsGrpsToDash() GroupsToDash {
	return d.BuildDashboard
}

type DashWriter func(context.Context, ma.Dashboard) error

type DashSer func(ma.Dashboard) ([]byte, error)

func DashToJSON(dash ma.Dashboard) ([]byte, error) { return json.Marshal(dash) }

var DashSerJSON DashSer = DashToJSON

type RawWriter func(context.Context, []byte) error

func (w RawWriter) ToDashWriter(conv DashSer) DashWriter {
	return func(ctx context.Context, dash ma.Dashboard) error {
		serialized, err := conv(dash)
		if nil != err {
			return err
		}

		return w(ctx, serialized)
	}
}

type IOWriter struct{ io.Writer }

func (w IOWriter) ToRawWriter() RawWriter {
	return func(_ context.Context, dat []byte) error {
		_, err := w.Writer.Write(dat)
		return err
	}
}

func (s DashSource) ToWriter(ctx context.Context, wtr DashWriter) error {
	dash, err := s(ctx)
	if nil != err {
		return err
	}

	return wtr(ctx, dash)
}
