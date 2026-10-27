<?php

declare(strict_types=1);

namespace Isureal\Tests\Unit;

use Isureal\Time;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

final class TimeTest extends TestCase
{
    public function testFormatTrimsFraction(): void
    {
        self::assertSame('2026-10-24T03:00:00.12Z', Time::format(Time::fromDb('2026-10-24 03:00:00.12+00')));
        self::assertSame('2026-10-24T03:00:00Z', Time::format(Time::fromDb('2026-10-24 03:00:00+00')));
        self::assertSame('2026-10-24T03:00:00.000001Z', Time::format(Time::fromDb('2026-10-24 12:00:00.000001+09')));
    }

    public function testParseRfc3339(): void
    {
        self::assertSame('2026-10-24T03:00:00.123456Z', Time::format(Time::parseRfc3339('2026-10-24T12:00:00.123456789+09:00') ?? self::fail()));
        self::assertSame('2026-10-24T12:00:00Z', Time::format(Time::parseRfc3339('2026-10-24T12:00:00Z') ?? self::fail()));
    }

    #[DataProvider('invalidTimes')]
    public function testRejectsInvalidRfc3339(string $value): void
    {
        self::assertNull(Time::parseRfc3339($value));
    }

    /**
     * @return iterable<array{string}>
     */
    public static function invalidTimes(): iterable
    {
        yield [''];
        yield ['2026-10-24'];
        yield ['2026-10-24 12:00:00Z'];
        yield ['2026-02-30T00:00:00Z'];
        yield ['2026-10-24T24:00:00Z'];
        yield ['2026-10-24T12:00:00'];
        yield ['2026-10-24T12:00:00+24:00'];
        yield ["2026-10-24T12:00:00Z\n"];
    }

    public function testParseDate(): void
    {
        self::assertSame('2026-09-23T00:00:00Z', Time::format(Time::parseDate('2026-09-23') ?? self::fail()));
        self::assertNull(Time::parseDate('2026-9-23'));
        self::assertNull(Time::parseDate('2026-02-29'));
        self::assertNull(Time::parseDate('2026-09-23T00:00:00Z'));
    }

    public function testDiffMicroseconds(): void
    {
        self::assertSame(1_500_000, Time::diffMicroseconds(Time::fromDb('2026-10-24 03:00:01.5+00'), Time::fromDb('2026-10-24 03:00:00+00')));
    }
}
