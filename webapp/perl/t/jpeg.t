use v5.44;
use utf8;
use Test2::V0;
use Imager;
use lib 't/lib';
use Isureal::JPEG qw(decode_jpeg);
use Isureal::Test qw(jpeg_data);

ok $Imager::formats{jpeg}, 'Imager can read JPEG';

my sub encode ($image, %options) {
    $image->write(data => \my $data, type => 'jpeg', %options) or die $image->errstr;
    return $data;
}

my $photo = jpeg_data(300, 200);
is decode_jpeg($photo), { width => 300, height => 200 }, 'baseline';
is decode_jpeg(encode(Imager->new(xsize => 30, ysize => 20), jpeg_progressive => 1)), { width => 30, height => 20 }, 'progressive';
is decode_jpeg(encode(Imager->new(xsize => 30, ysize => 20, channels => 1))), { width => 30, height => 20 }, 'grayscale';
is decode_jpeg($photo . 'trailing bytes'), { width => 300, height => 200 }, 'bytes after EOI are ignored';

my $png;
Imager->new(xsize => 8, ysize => 8)->write(data => \$png, type => 'png') or die Imager->errstr;
for my $case (
    ['empty'            => ''],
    ['garbage'          => 'not a jpeg'],
    ['PNG'              => $png],
    ['SOI and EOI only' => "\xff\xd8\xff\xd9"],
    # 途中で切れたデータは libjpeg が警告して補うが、Go の image/jpeg と同じく受け付けない
    ['truncated'        => substr($photo, 0, length($photo) / 2)],
    ['header only'      => substr($photo, 0, 700)],
) {
    my ($name, $data) = @$case;
    is decode_jpeg($data), undef, $name;
}

# 拒否のあとに受け付ける画像を読んでも、前の警告が残らない
is decode_jpeg($photo), { width => 300, height => 200 }, 'decode after a rejection';

done_testing;
