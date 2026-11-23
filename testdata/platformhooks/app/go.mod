module example.com/platformhooks/app

go 1.22

require example.com/platformhooks/board v0.0.0

replace (
	example.com/platformhooks/board => ../board
	example.com/platformhooks/soc => ../soc
)
