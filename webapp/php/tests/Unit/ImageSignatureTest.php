<?php

declare(strict_types=1);

namespace Isureal\Tests\Unit;

use Isureal\ImageSignature;
use Isureal\Model\User;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

final class ImageSignatureTest extends TestCase
{
    public function testSignatureVector(): void
    {
        // Python の hashlib/hmac で1000回反復して求めた値と照合する（webapp/go と同じ）
        $key = hash('sha256', 'test-secret', true);
        $signature = ImageSignature::sign($key, '550e8400-e29b-41d4-a716-446655440000', 1790553600);
        self::assertSame('qv9Jrl_ciG5MowUjgzZ4_bYd2cxIxvpS7zOcTSqxx84', ImageSignature::base64UrlEncode($signature));
        self::assertSame($key, ImageSignature::signingKey(bin2hex($key)));
    }

    #[DataProvider('invalidKeys')]
    public function testRejectsInvalidStoredKey(string $hash): void
    {
        $this->expectException(\UnexpectedValueException::class);
        ImageSignature::signingKey($hash);
    }

    /**
     * @return iterable<array{string}>
     */
    public static function invalidKeys(): iterable
    {
        yield [''];
        yield ['hash'];
        yield ['abcd'];
    }

    public function testSignedUrlLifetime(): void
    {
        $user = new User('owner', 'owner', hash('sha256', 'test-secret'), 'Owner', '', null, 'asia', 'regular', 'active');
        $before = time();
        $url = ImageSignature::url($user, 'image-id');
        $after = time();

        self::assertSame('/api/images/image-id', parse_url($url, PHP_URL_PATH));
        parse_str((string) parse_url($url, PHP_URL_QUERY), $query);
        self::assertSame(['exp', 'sig'], array_keys($query));
        $exp = (int) $query['exp'];
        self::assertGreaterThanOrEqual($before + 86400, $exp);
        self::assertLessThanOrEqual($after + 86400, $exp);
        self::assertSame(ImageSignature::base64UrlEncode(ImageSignature::sign($user->imageKey(), 'image-id', $exp)), $query['sig']);
    }

    public function testBase64UrlDecodeIsStrict(): void
    {
        $encoded = ImageSignature::base64UrlEncode(str_repeat("\xff", 32));
        self::assertSame(str_repeat("\xff", 32), ImageSignature::base64UrlDecode($encoded));
        self::assertNull(ImageSignature::base64UrlDecode($encoded . '='));
        self::assertNull(ImageSignature::base64UrlDecode(strtr($encoded, '_', '/')));
        // 余りのビットが 0 でない
        self::assertNull(ImageSignature::base64UrlDecode(substr($encoded, 0, -1) . '9'));
    }
}
