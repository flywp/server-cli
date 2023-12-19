<?php

declare(strict_types=1);

namespace Fly;

class Printer
{
    public function out($message)
    {
        echo $message;
    }

    public function newline()
    {
        $this->out("\n");
    }

    public function display($message)
    {
        $this->out($message);
        $this->newline();
    }
}
