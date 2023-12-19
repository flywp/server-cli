<?php

declare(strict_types=1);

require_once __DIR__ . '/vendor/autoload.php';

use Fly\App;

$app = new App();

$app->registerCommand('hello', function (array $argv) use ($app) {
    $name = isset($argv[2]) ? $argv[2] : 'World';
    $app->getPrinter()->display("Hello {$name}!");
});

$app->registerCommand('help', function (array $argv) use ($app) {
    $app->getPrinter()->display('usage: fly hello [ your-name ]');
});

$app->runCommand($argv);
