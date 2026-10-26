<?php

declare(strict_types=1);

namespace Isureal\Tests\Unit;

use Isureal\Jpeg;
use PHPUnit\Framework\TestCase;

final class JpegTest extends TestCase
{
    public function testSize(): void
    {
        self::assertSame([3, 2], Jpeg::size(self::encode(3, 2)));
        self::assertSame([4097, 1], Jpeg::size(self::encode(4097, 1)));
        // プログレッシブも受け付ける
        $progressive = self::encode(16, 8, progressive: true);
        self::assertStringContainsString("\xff\xc2", $progressive);
        self::assertSame([16, 8], Jpeg::size($progressive));
        // EOI の後ろの余分なバイトは無視する（Go の image/jpeg と同じ）
        self::assertSame([3, 2], Jpeg::size(self::encode(3, 2) . 'trailing bytes'));
    }

    public function testRejectsInvalidData(): void
    {
        $valid = self::encode(64, 64);
        self::assertNull(Jpeg::size(''));
        self::assertNull(Jpeg::size('image bytes'));
        self::assertNull(Jpeg::size("\xff\xd8\xff\xd9"));
        // 途中で切れたもの（Go の image/jpeg も受け付けない）
        self::assertNull(Jpeg::size(substr($valid, 0, intdiv(strlen($valid), 2))));
        self::assertNull(Jpeg::size(substr($valid, 0, -2)));
        $image = imagecreatetruecolor(2, 2) ?: throw new \RuntimeException('imagecreatetruecolor failed');
        ob_start();
        imagepng($image);
        self::assertNull(Jpeg::size((string) ob_get_clean()));
    }

    /**
     * @param int<1, max> $width
     * @param int<1, max> $height
     */
    private static function encode(int $width, int $height, bool $progressive = false): string
    {
        $image = imagecreatetruecolor($width, $height) ?: throw new \RuntimeException('imagecreatetruecolor failed');
        for ($x = 0; $x < $width; $x++) {
            imagesetpixel($image, $x, 0, (int) imagecolorallocate($image, ($x * 37) % 256, ($x * 11) % 256, 200));
        }
        imageinterlace($image, $progressive);
        ob_start();
        imagejpeg($image);
        return (string) ob_get_clean();
    }
}
