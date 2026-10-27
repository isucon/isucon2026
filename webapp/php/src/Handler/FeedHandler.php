<?php

declare(strict_types=1);

namespace Isureal\Handler;

use Isureal\Database;
use Isureal\Http\ApiError;
use Isureal\Http\Input;
use Isureal\Http\Json;
use Isureal\Http\Middleware\SessionMiddleware;
use Isureal\Model\Post;
use Isureal\Model\ViewerContext;
use Isureal\Model\Visibility;
use Isureal\Repository\PostRepository;
use Isureal\Time;
use Psr\Http\Message\ResponseInterface as Response;
use Psr\Http\Message\ServerRequestInterface as Request;

final readonly class FeedHandler
{
    private const array FEEDS = ['friends', 'friends-of-friends', 'public', 'following'];

    public function __construct(private Database $db, private PostRepository $posts)
    {
    }

    /**
     * GET /api/feeds/{feed}
     *
     * @param array<string, string> $args
     */
    public function show(Request $request, Response $response, array $args): Response
    {
        $feed = $args['feed'];
        $beforePostedAt = Input::queryTime($request, 'beforePostedAt');
        $beforeId = Input::queryString($request, 'beforeId');
        if (!in_array($feed, self::FEEDS, true)) {
            throw new ApiError(400, 'invalid_post');
        }
        if (($beforePostedAt === null) !== ($beforeId === '')) {
            throw new ApiError(400, 'invalid_pagination');
        }
        $viewer = $this->posts->loadViewer(SessionMiddleware::session($request)->userId);
        if ($viewer === null) {
            return Json::respond($response, ['items' => [], 'hasMore' => false]);
        }
        $following = [];
        if ($feed === 'following') {
            $followeeIds = $this->db->fetchColumn('SELECT followee_id FROM follows WHERE follower_id = :follower_id', ['follower_id' => $viewer->user->id]);
            $following = array_fill_keys($followeeIds, true);
        }
        $posts = $this->posts->selectWithAuthor(
            'AND p.author_id <> :viewer_id AND w.ended_at IS NULL
             AND (:before_posted_at::timestamptz IS NULL OR (p.posted_at, p.id) < (:before_posted_at, :before_id))
             ORDER BY p.posted_at DESC, p.id DESC',
            [
                'viewer_id' => $viewer->user->id,
                'before_posted_at' => $beforePostedAt === null ? null : Time::toDb($beforePostedAt),
                'before_id' => $beforeId,
            ],
        );
        $items = [];
        foreach ($posts as [$post, $author]) {
            if (!$viewer->canView($post) || !self::matches($viewer, $following, $post, $feed)) {
                continue;
            }
            $items[] = $post->toResponse($author);
            if (count($items) === 51) {
                break;
            }
        }
        return Json::respond($response, UserHandler::page($items));
    }

    /**
     * @param array<string, true> $following
     */
    private static function matches(ViewerContext $viewer, array $following, Post $post, string $feed): bool
    {
        return match ($feed) {
            'public' => $post->visibility === Visibility::Public->value,
            'following' => isset($following[$post->authorId]),
            'friends' => $viewer->isFriend($post->authorId),
            'friends-of-friends' => !$viewer->isFriend($post->authorId) && $viewer->isFriendOfFriend($post->authorId),
            default => false,
        };
    }
}
