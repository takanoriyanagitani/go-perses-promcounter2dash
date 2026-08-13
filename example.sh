#!/bin/bash

wsm="./promcounter2dash.wasm"

export ENV_METRICS_DATA_SIZE_MAX=16777216
export ENV_GROUP_NAME="group_g"
export ENV_PROJECT_NAME="project_p"
export ENV_DASHBOARD_NAME="dashboard_d"

export ENV_TRUSTED_STATIC_FILTER='{job="job1"}'

input1(){
	echo '# HELP nvme_power_cycles_total SMART metric power_cycles_total'
	echo '# TYPE nvme_power_cycles_total counter'
	echo 'nvme_power_cycles_total{device="nvme0n1"} 28'
	echo 'nvme_power_cycles_total{device="nvme1n1"} 38'

	echo '# HELP nvme_power_on_hours_total SMART metric power_on_hours_total'
	echo '# TYPE nvme_power_on_hours_total counter'
	echo 'nvme_power_on_hours_total{device="nvme0n1"} 464'
	echo 'nvme_power_on_hours_total{device="nvme1n1"} 34026'
}

input1 |
	\time -l wasmtime run \
	  --wasm max-memory-size=167772160 \
		--env ENV_METRICS_DATA_SIZE_MAX=${ENV_METRICS_DATA_SIZE_MAX} \
		--env ENV_GROUP_NAME="${ENV_GROUP_NAME}" \
		--env ENV_PROJECT_NAME="${ENV_PROJECT_NAME}" \
		--env ENV_DASHBOARD_NAME="${ENV_DASHBOARD_NAME}" \
		--env ENV_TRUSTED_STATIC_FILTER="${ENV_TRUSTED_STATIC_FILTER}" \
		"${wsm}" |
	dasel --in=json --out=yaml |
	bat --language=yaml
