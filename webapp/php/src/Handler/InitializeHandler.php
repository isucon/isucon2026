<?php

declare(strict_types=1);

namespace Isureal\Handler;

use Isureal\Http\Json;
use Psr\Http\Message\ResponseInterface as Response;
use Psr\Http\Message\ServerRequestInterface as Request;

final class InitializeHandler
{
    private const string INIT_SCRIPT = __DIR__ . '/../../../sql/init.sh';

    /**
     * POST /api/initialize
     */
    public function __invoke(Request $request, Response $response): Response
    {
        // 出力は php-fpm のログに流す
        $log = fopen('php://stderr', 'w') ?: throw new \RuntimeException('failed to open stderr');
        $process = proc_open([self::INIT_SCRIPT], [1 => $log, 2 => $log], $pipes);
        if ($process === false || ($status = proc_close($process)) !== 0) {
            throw new \RuntimeException('failed to run ' . self::INIT_SCRIPT . ': exit status ' . ($status ?? -1));
        }
        return Json::respond($response, ['language' => 'php']);
    }
}
