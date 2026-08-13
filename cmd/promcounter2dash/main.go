package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"

	pd "github.com/takanoriyanagitani/go-perses-promcounter2dash"
)

var smetSizeMax string = os.Getenv("ENV_METRICS_DATA_SIZE_MAX")

var staticFilterTrusted pd.StaticFilter = pd.SimpleStaticFilter(
	os.Getenv("ENV_TRUSTED_STATIC_FILTER"),
)

var metReader io.Reader = os.Stdin

var p2bldr pd.PromParserBuilder = pd.PromParserBuilderEmpty.
	Legacy()

var parser pd.PromParser = p2bldr.ToParser()
var b2m pd.BytesToMet = parser.ToBytesToMet()

var singleGroupName string = os.Getenv("ENV_GROUP_NAME")

var dashName string = os.Getenv("ENV_DASHBOARD_NAME")
var projName string = os.Getenv("ENV_PROJECT_NAME")

var rate pd.Rate = pd.
	RateDefault.
	WithFilter(staticFilterTrusted)

var f2po pd.FamilyToPanelOpt = rate.ToFamilyToPanelOptTSChart()

var dbldr pd.DashboardBuilder = pd.DashboardBuilder{
	DashboardName:    pd.DashboardName(dashName),
	ProjectName:      pd.ProjectName(projName),
	FamilyToPanelOpt: f2po,
}

var dser pd.DashSer = pd.DashSerJSON

func sub(ctx context.Context) error {
	slog.Info("setting up", "ENV_METRICS_DATA_SIZE_MAX", smetSizeMax)
	slog.Info("setting up", "ENV_GROUP_NAME", singleGroupName)
	slog.Info("setting up", "ENV_DASHBOARD_NAME", dashName)
	slog.Info("setting up", "ENV_PROJECT_NAME", projName)
	slog.Info("setting up", "ENV_TRUSTED_STATIC_FILTER", staticFilterTrusted)

	imetSizeMax, err := strconv.Atoi(smetSizeMax)
	if nil != err {
		return err
	}

	var rdr2rms func(io.Reader) pd.RawMetSource = pd.ReaderToRawMetSource(
		int64(imetSizeMax),
	)

	var rmsrc pd.RawMetSource = rdr2rms(metReader)
	var msrc pd.MetSource = rmsrc.ToMetSource(b2m)

	var grouper pd.Grouper = pd.SingleGroupName(singleGroupName).ToGrouper()
	var gsrc pd.GroupsSource = msrc.ToGroupsSource(grouper)

	var g2d pd.GroupsToDash = dbldr.AsGrpsToDash()

	var dsrc pd.DashSource = gsrc.ToDashSource(g2d)

	var bwtr *bufio.Writer = bufio.NewWriter(os.Stdout)

	var rwtr pd.RawWriter = pd.IOWriter{Writer: bwtr}.ToRawWriter()
	var dwtr pd.DashWriter = rwtr.ToDashWriter(dser)

	slog.Info("configured", "input metrics size max", imetSizeMax)

	return errors.Join(
		dsrc.ToWriter(ctx, dwtr),
		bwtr.Flush(),
	)
}

func main() {
	err := sub(context.Background())
	if nil != err {
		slog.Error("error got", "error", err)
	}
}
