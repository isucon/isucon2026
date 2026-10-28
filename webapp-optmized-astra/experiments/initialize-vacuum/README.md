# 初期化後のVACUUMと統計更新

実機でrealmojisに115,197件、postsに109,112件、commentsに92,292件、notificationsに65,707件の削除済み行が残っていた。通常の初期化はDELETE後にCOMMITするだけで、これらの処理をautovacuumに任せていた。

reset.sqlのトランザクション終了後に対象4テーブルの `VACUUM (ANALYZE)` を行い、小さい関連テーブルもANALYZEする。Rustで検索正規化テーブルを再構築した後に、その統計も更新する。データの復元処理は同じ。初期化の60秒制限内に終わることと、実機スコアで効果を確認する。

[PostgreSQL 16の説明](https://www.postgresql.org/docs/16/routine-vacuuming.html)では、通常のVACUUMは削除済み行の領域を再利用可能にし、可視性マップを更新する。VACUUM自体のI/O負荷もあるため、今回の実験では負荷走行前の初期化に置く。FULLは指定しない。

現時点では候補であり未配備。
