<?php

declare(strict_types=1);

namespace Isureal\Tests\Unit;

use Isureal\Handler\UserHandler;
use Isureal\Http\ApiError;
use Isureal\Http\Input;
use PHPUnit\Framework\TestCase;
use Slim\Psr7\Factory\ServerRequestFactory;

final class InputTest extends TestCase
{
    public function testQueryUsesFirstValue(): void
    {
        $query = 'q=a+b&q=c&empty=&bad=%zz';
        $request = new ServerRequestFactory()->createServerRequest('GET', "/api/users?{$query}", ['QUERY_STRING' => $query]);
        self::assertSame('a b', Input::query($request, 'q'));
        self::assertSame(['a b', 'c'], Input::queryValues($request, 'q'));
        self::assertSame('', Input::query($request, 'empty'));
        self::assertNull(Input::query($request, 'bad'));
        self::assertNull(Input::query($request, 'missing'));
    }

    public function testStrictQuery(): void
    {
        self::assertSame(['exp' => ['1'], 'sig' => ['x']], Input::parseQuery('exp=1&sig=x', strict: true));
        self::assertNull(Input::parseQuery('exp=1&sig=%zz', strict: true));
        self::assertNull(Input::parseQuery('exp=1;sig=x', strict: true));
    }

    public function testQueryTime(): void
    {
        $request = new ServerRequestFactory()->createServerRequest('GET', '/api/feeds/public?beforePostedAt=2026-10-24T12:00:00Z&bad=2026-10-24');
        self::assertNotNull(Input::queryTime($request, 'beforePostedAt'));
        self::assertNull(Input::queryTime($request, 'missing'));
        $this->expectException(ApiError::class);
        Input::queryTime($request, 'bad');
    }

    public function testFormNumber(): void
    {
        self::assertNull(Input::formNumber(null));
        self::assertSame(0.0, Input::formNumber(''));
        self::assertSame(35.5, Input::formNumber('35.5'));
        $this->expectException(ApiError::class);
        Input::formNumber(' 35');
    }

    public function testNormalizeSearchText(): void
    {
        self::assertSame('strasse', UserHandler::normalizeSearchText('STRASSE'));
        self::assertSame(UserHandler::normalizeSearchText('straße'), UserHandler::normalizeSearchText('STRASSE'));
        self::assertSame("\u{e9}", UserHandler::normalizeSearchText("E\u{301}"));
    }
}
