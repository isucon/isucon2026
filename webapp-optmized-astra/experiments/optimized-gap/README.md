# Go最適化版とTeam 12 Rust版の差

同じ固定ベンチイメージ（sha256:55649c0a…）・seed 1・large・merged.zipの既存走行を比較する。対象チームへの新しい配備やベンチは行わず、既存ログを使用した。現行Rust版はf9aeafc9、179,991点PASS。比較対象のmeta.jsonのcommitはベンチ実行側のチェックアウトであり、配備済みGoバイナリのコミットを保証しない。

| 実装・環境 | スコア（すべてPASS） | Asia app / db1 | router |
|---|---:|---|---|
| Go・Team 11 | 323,803 / 325,916 | c7i.large / c6in.large | c6in.large |
| Go・Team 9 | 304,765 / 320,614 | c5.large / c5.large | c6i.large |
| Rust・Team 12 | 179,991 | c5.large / c5.large | c6in.large |

全例でEU・US appはc7i.large、ベンチは4 vCPU。Team 9はapp/dbの型とEBSのIOPS・帯域設定がTeam 12と同じで、ルーターの型は異なる。VM実体・走行時刻・キャッシュ状態・データの同一性までは揃っていないため、差分をそのまま変更による改善見込みとは扱わない。

Team 9の320,614点との差140,623点のうち、月別履歴42,054点・日別履歴8,840点・export 33,720点で84,614点（60.2%）を占める。比較したGoの4走すべてで、履歴とexportの寄与は点差の60.2〜63.4%。署名検証やZIP出力だけでは、この差は埋まらない。

Asiaの中央値はGo Team 9 / Rust Team 12で、Memories 4.0 / 187.9ms、日別履歴2.7 / 156.4ms、画像7.4 / 90.0ms、自分の情報17.6 / 240.4ms、友達の友達フィード19.8 / 215.0ms。投稿は逆に716.7 / 457.3msでRustの中央値が低い。全APIが一律に遅いのではなく、読み取り、とくにAsiaの初期履歴で差が大きい。

Team 12のAsia app→db1のidle時pingは4回平均0.487ms（0.466〜0.502ms）。Memoriesの188msを固定ネットワーク遅延だけでは説明できない。pg_stat_statementsは導入されておらず、追加の拡張や設定変更は行わない。読み取り専用のpg_stat_activityサンプルで負荷時の実行SQLと待ちを調べる。

比較コードはcompare.py、再計算結果はcomparison.json、Goの元資料はbenchmarks/に保存した。Rustの元資料は../../results/20261029T131830Z-team12/。SQLサンプラーはsample_metadata.py。プロファイル付き走行は未実施。
