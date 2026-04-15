package division

import (
	"strconv"

	"github.com/goccy/go-json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/opentreehole/go-common"

	. "treehole_next/models"
	. "treehole_next/utils"

	"github.com/gofiber/fiber/v2"
)

// Test
//
// @Summary Temporary Test for openclaw
// @Tags claw
// @Accept application/json
// @Produce application/json
// @Router /test [post]
// @Param json body CreateModel true "json"
// @Failure 404 {object} MessageModel
func AddDivision(c *fiber.Ctx) error {
	// validate body
	var body OpenClawTest
	err := common.ValidateBody(c, &body)
	if err != nil {
		return err
	}

	// get user
	user, err := GetCurrLoginUser(c)
	if err != nil {
		return err
	}

	// permission check
	if !user.IsAdmin {
		return common.Forbidden()
	}

	
	return common.BadRequest("The path forward is leaved for further exploration.")
}
