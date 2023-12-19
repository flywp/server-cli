<?php

declare(strict_types=1);

namespace Fly;

class App
{
    protected $printer;

    protected $registry = [];

    public function __construct()
    {
        $this->printer = new Printer();
    }

    public function getPrinter()
    {
        return $this->printer;
    }

    public function registerCommand($name, $callable)
    {
        $this->registry[$name] = $callable;
    }

    public function getCommand($command)
    {
        return isset($this->registry[$command]) ? $this->registry[$command] : null;
    }

    public function runCommand(array $argv = [])
    {
        $command = "help";

        if (isset($argv[1])) {
            $command = $argv[1];
        }

        $command = $this->getCommand($command);

        if ($command === null) {
            $this->getPrinter()->display("ERROR: Command \"$command\" not found.");
            exit;
        }

        call_user_func($command, $argv);
    }
}
