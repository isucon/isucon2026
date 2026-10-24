use v5.44;
use utf8;
use Test2::V0;
use Digest::SHA qw(sha256 sha256_hex);
use MIME::Base64 qw(encode_base64url);
use Mojo::URL;
use Isureal::ImageSignature qw(image_signing_key image_signature signed_image_url);

subtest 'signature vector' => sub {
    # Go 版と同じく、Python の hashlib/hmac で1000回反復して求めた値と照合する。
    my $key = sha256('test-secret');
    is encode_base64url(image_signature($key, '550e8400-e29b-41d4-a716-446655440000', 1790553600)),
        'qv9Jrl_ciG5MowUjgzZ4_bYd2cxIxvpS7zOcTSqxx84';
    is image_signing_key(sha256_hex('test-secret')), $key;
    like dies { image_signing_key($_) }, qr/invalid image signing key/, "reject '$_'" for '', 'hash', 'abcd';
};

subtest 'signed URL lifetime' => sub {
    my $key    = sha256('test-secret');
    my $before = time;
    my $url    = Mojo::URL->new(signed_image_url({ image_key => $key }, 'image-id'));
    my $after  = time;
    my $exp    = $url->query->param('exp');
    ok $exp >= $before + 86400 && $exp <= $after + 86400, 'expires 24h after issuance';
    is $url->path->to_string, '/api/images/image-id';
    is $url->query->names, ['exp', 'sig'];
    is $url->query->param('sig'), encode_base64url(image_signature($key, 'image-id', $exp));
};

done_testing;
