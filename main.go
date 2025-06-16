package main

import (
	"context"
	"fmt"
	"time"
)

func main() {
	_, cancel := context.WithTimeout(context.Background(), time.Second*5)
	fmt.Println("hello")
	defer cancel()

}
