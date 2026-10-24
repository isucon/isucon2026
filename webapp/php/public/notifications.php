<?php

declare(strict_types=1);

// GET /api/notifications（nginx がこのファイルに振り分ける）
require __DIR__ . '/../vendor/autoload.php';

use Isureal\Config;
use Isureal\Notification\NotificationStream;
use Slim\Factory\ServerRequestCreatorFactory;

new NotificationStream(Config::fromEnv())->handle(ServerRequestCreatorFactory::create()->createServerRequestFromGlobals());
