<?php

declare(strict_types=1);

// テストで php -S に渡すルーター。nginx と同じく /api/notifications だけ notifications.php に振り分ける
if (parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH) === '/api/notifications') {
    require __DIR__ . '/../../public/notifications.php';
    return;
}
require __DIR__ . '/../../public/index.php';
