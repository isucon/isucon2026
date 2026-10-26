#!/bin/sh
# 1 秒ごとに CPU、メモリ、TCP、ネットワーク、ディスク、PSI、cgroup の累積値を、5 秒ごとにプロセスごとの
# CPU 時間を出す。
# 率は report.py が差分から計算する。ベンチのコンテナと、SSM 経由でチームのサーバーで動かす。
# 使い方: sampler.sh STOP_FILE MAX_SECONDS
stop="$1"
max="${2:-1800}"
cg=/sys/fs/cgroup

# cgroup v2 の cpu.max か v1 の cfs_quota_us から、CPU の上限（コア数）を出す
quota=""
if [ -r "$cg/cpu.max" ]; then
	quota="$(awk '$1 != "max" { printf "%.3f", $1 / $2 }' "$cg/cpu.max")"
elif [ -r "$cg/cpu/cpu.cfs_quota_us" ]; then
	quota="$(awk -v period="$(cat "$cg/cpu/cpu.cfs_period_us")" '$1 > 0 { printf "%.3f", $1 / period }' "$cg/cpu/cpu.cfs_quota_us")"
fi
echo "H ts=$(date +%s%3N) hz=$(getconf CLK_TCK) ncpu=$(nproc) page=$(getconf PAGESIZE) quota=${quota:-none} host=$(hostname)"
# NVMe のシリアル番号は EBS のボリューム ID（ハイフンなし）なので、ボリュームの上限と対応付けられる
for disk in /sys/block/nvme*n*; do
	[ -r "$disk/device/serial" ] && echo "D name=${disk##*/} serial=$(tr -d ' ' <"$disk/device/serial")"
done

i=0
while [ ! -e "$stop" ] && [ "$i" -lt "$max" ]; do
	ts="$(date +%s%3N)"
	{
		grep '^cpu ' /proc/stat
		grep -E '^(MemTotal|MemAvailable|Dirty|Writeback):' /proc/meminfo
		grep '^TCP:' /proc/net/sockstat
		tail -n +3 /proc/net/dev | sed 's/^/net /'
		sed 's/^/disk /' /proc/diskstats
		for resource in cpu io memory; do
			[ -r "/proc/pressure/$resource" ] && sed "s/^/psi $resource /" "/proc/pressure/$resource"
		done
		[ -r "$cg/cpu.stat" ] && sed 's/^/cg /' "$cg/cpu.stat"
		[ -r "$cg/cpu/cpu.stat" ] && sed 's/^/cg /' "$cg/cpu/cpu.stat"
		[ -r "$cg/cpuacct/cpuacct.usage" ] && echo "cg usage_ns $(cat "$cg/cpuacct/cpuacct.usage")"
		[ -r "$cg/memory.current" ] && echo "cg memory_bytes $(cat "$cg/memory.current")"
		[ -r "$cg/memory/memory.usage_in_bytes" ] && echo "cg memory_bytes $(cat "$cg/memory/memory.usage_in_bytes")"
	} 2>/dev/null | awk -v ts="$ts" '
		$1 == "cpu" { cpu = $2 "," $3 "," $4 "," $5 "," $6 "," $7 "," $8 "," $9 }
		$1 == "MemTotal:" { total = $2 }
		$1 == "MemAvailable:" { avail = $2 }
		$1 == "Dirty:" { dirty = $2 }
		$1 == "Writeback:" { writeback = $2 }
		$1 == "TCP:" { tcp = $3 "," $7 }
		$1 == "net" {
			line = $0
			sub(/^net +/, "", line)
			sub(/:/, " ", line)
			split(line, f, " ")
			if (f[1] != "lo") net = net " net." f[1] "=" f[2] "," f[10]
		}
		$1 == "cg" { cgs = cgs " cg." $2 "=" $3 }
		# パーティションは数えない。読み取り回数、読み取りセクタ、読み取り ms、書き込み回数、書き込みセクタ、
		# 書き込み ms、処理中の数、I/O 中の ms、待ち時間の重み付き ms
		$1 == "disk" && $4 ~ /^(nvme[0-9]+n[0-9]+|xvd[a-z]+|sd[a-z]+|vd[a-z]+)$/ {
			disks = disks " disk." $4 "=" $5 "," $7 "," $8 "," $9 "," $11 "," $12 "," $13 "," $14 "," $15
		}
		# 待たされた時間の累積（マイクロ秒）
		$1 == "psi" { split($NF, kv, "="); psi = psi " psi." $2 "." $3 "=" kv[2] }
		END { print "S ts=" ts " cpu=" cpu " mem=" total "," avail "," dirty "," writeback " tcp=" tcp cgs net disks psi }'
	if [ $((i % 5)) -eq 0 ]; then
		# comm に空白や括弧が入りうるので、最後の ")" より後ろを数える（utime=14、stime=15、cutime=16、
		# cstime=17、rss=24 番目）。終了した子の時間は親の cutime/cstime に入るので、短命なプロセスも数えられる
		cat /proc/[0-9]*/stat 2>/dev/null | awk -v ts="$ts" '{
			lp = index($0, "(")
			rp = 0
			for (k = length($0); k > lp; k--) if (substr($0, k, 1) == ")") { rp = k; break }
			comm = substr($0, lp + 1, rp - lp - 1)
			gsub(/[ \t]/, "_", comm)
			split(substr($0, rp + 2), f, " ")
			ticks = f[12] + f[13] + f[14] + f[15]
			if (ticks > 0) print "P ts=" ts " pid=" substr($0, 1, lp - 2) " comm=" comm " ticks=" ticks " rss=" f[22]
		}'
	fi
	i=$((i + 1))
	sleep 1
done
