<?php

declare(strict_types=1);

namespace Isureal;

use DI\ContainerBuilder;
use Isureal\Http\ErrorHandler;
use Isureal\Model\Region;
use Slim\App;
use Slim\Factory\AppFactory;

final class Application
{
    /**
     * @param array<string, mixed> $definitions app/dependencies.php を上書きする定義（テスト用）
     * @return App<\DI\Container>
     */
    public static function create(array $definitions = []): App
    {
        $builder = new ContainerBuilder();
        $builder->addDefinitions(__DIR__ . '/../app/dependencies.php', $definitions);
        $container = $builder->build();

        $config = $container->get(Config::class);
        if (!Region::isValid($config->region)) {
            throw new \RuntimeException("invalid server region: {$config->region}");
        }

        $app = AppFactory::createFromContainer($container);
        (require __DIR__ . '/../app/routes.php')($app);
        $app->addRoutingMiddleware();
        $app->addErrorMiddleware(false, false, false)
            ->setDefaultErrorHandler(new ErrorHandler($app->getResponseFactory()));
        return $app;
    }
}
