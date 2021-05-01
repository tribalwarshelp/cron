package tasks

import (
	"context"
	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
	"github.com/pkg/errors"
	"github.com/tribalwarshelp/shared/models"
	"github.com/tribalwarshelp/shared/tw/dataloader"
	"time"
)

type taskUpdateServerData struct {
	*task
}

func (t *taskUpdateServerData) execute(url string, server *models.Server) error {
	if err := t.validatePayload(server); err != nil {
		log.Debug(err)
		return nil
	}
	now := time.Now()
	entry := log.WithField("key", server.Key)
	entry.Infof("taskUpdateServerData.execute: %s: Update of the server data has started...", server.Key)
	err := (&workerUpdateServerData{
		db:         t.db.WithParam("SERVER", pg.Safe(server.Key)),
		dataloader: newDataloader(url),
		server:     server,
	}).update()
	if err != nil {
		err = errors.Wrap(err, "taskUpdateServerData.execute")
		entry.Error(err)
		return err
	}
	duration := time.Since(now)
	entry.
		WithFields(map[string]interface{}{
			"duration":       duration.Nanoseconds(),
			"durationPretty": duration.String(),
		}).
		Infof("taskUpdateServerData.execute: %s: data has been updated", server.Key)
	return nil
}

func (t *taskUpdateServerData) validatePayload(server *models.Server) error {
	if server == nil {
		return errors.New("taskUpdateServerData.validatePayload: Expected *models.Server, got nil")
	}

	return nil
}

type workerUpdateServerData struct {
	db         *pg.DB
	dataloader dataloader.DataLoader
	server     *models.Server
}

type loadPlayersResult struct {
	ids             []int
	players         []*models.Player
	playersToServer []*models.PlayerToServer
	deletedPlayers  []int
	numberOfPlayers int
}

func (w *workerUpdateServerData) loadPlayers(od map[int]*models.OpponentsDefeated) (loadPlayersResult, error) {
	var ennoblements []*models.Ennoblement
	result := loadPlayersResult{}
	if err := w.db.
		Model(&ennoblements).
		DistinctOn("new_owner_id").
		Order("new_owner_id ASC", "ennobled_at ASC").
		Select(); err != nil {
		return result, errors.Wrap(err, "workerUpdateServerData.loadPlayers: couldn't load ennoblements")
	}

	var err error
	result.players, err = w.dataloader.LoadPlayers()
	if err != nil {
		return result, err
	}
	result.numberOfPlayers = len(result.players)

	now := time.Now()
	result.playersToServer = make([]*models.PlayerToServer, result.numberOfPlayers)
	result.ids = make([]int, result.numberOfPlayers)
	searchableByNewOwnerID := &ennoblementsSearchableByNewOwnerID{ennoblements}
	for index, player := range result.players {
		playerOD, ok := od[player.ID]
		if ok {
			player.OpponentsDefeated = *playerOD
		}

		firstEnnoblementIndex := searchByID(searchableByNewOwnerID, player.ID)
		if firstEnnoblementIndex >= 0 {
			firstEnnoblement := ennoblements[firstEnnoblementIndex]
			diffInDays := getDateDifferenceInDays(now, firstEnnoblement.EnnobledAt)
			player.DailyGrowth = calcPlayerDailyGrowth(diffInDays, player.Points)
		}

		result.playersToServer[index] = &models.PlayerToServer{
			PlayerID:  player.ID,
			ServerKey: w.server.Key,
		}

		result.ids[index] = player.ID
	}

	searchableNewPlayers := &playersSearchableByID{result.players}
	if err := w.db.
		Model(&models.Player{}).
		Column("id").
		Where("exists = true").
		ForEach(func(player *models.Player) error {
			if index := searchByID(searchableNewPlayers, player.ID); index < 0 {
				result.deletedPlayers = append(result.deletedPlayers, player.ID)
			}
			return nil
		}); err != nil {
		return result, errors.Wrap(err, "workerUpdateServerData.loadPlayers: Players that have been deleted couldn't be detected")
	}

	return result, nil
}

func (w *workerUpdateServerData) loadTribes(od map[int]*models.OpponentsDefeated, numberOfVillages int) ([]*models.Tribe, error) {
	tribes, err := w.dataloader.LoadTribes()
	if err != nil {
		return nil, err
	}
	for _, tribe := range tribes {
		tribeOD, ok := od[tribe.ID]
		if ok {
			tribe.OpponentsDefeated = *tribeOD
		}
		if tribe.TotalVillages > 0 && numberOfVillages > 0 {
			tribe.Dominance = float64(tribe.TotalVillages) / float64(numberOfVillages) * 100
		} else {
			tribe.Dominance = 0
		}
	}
	return tribes, nil
}

func (w *workerUpdateServerData) calculateODifference(od1 models.OpponentsDefeated, od2 models.OpponentsDefeated) models.OpponentsDefeated {
	return models.OpponentsDefeated{
		RankAtt:    (od1.RankAtt - od2.RankAtt) * -1,
		ScoreAtt:   od1.ScoreAtt - od2.ScoreAtt,
		RankDef:    (od1.RankDef - od2.RankDef) * -1,
		ScoreDef:   od1.ScoreDef - od2.ScoreDef,
		RankSup:    (od1.RankSup - od2.RankSup) * -1,
		ScoreSup:   od1.ScoreSup - od2.ScoreSup,
		RankTotal:  (od1.RankTotal - od2.RankTotal) * -1,
		ScoreTotal: od1.ScoreTotal - od2.ScoreTotal,
	}
}

func (w *workerUpdateServerData) calculateTodaysTribeStats(
	tribes []*models.Tribe,
	history []*models.TribeHistory,
) []*models.DailyTribeStats {
	var todaysStats []*models.DailyTribeStats
	searchableTribes := makeTribesSearchable(tribes)

	for _, historyRecord := range history {
		if index := searchByID(searchableTribes, historyRecord.TribeID); index != -1 {
			tribe := tribes[index]
			todaysStats = append(todaysStats, &models.DailyTribeStats{
				TribeID:           tribe.ID,
				Members:           tribe.TotalMembers - historyRecord.TotalMembers,
				Villages:          tribe.TotalVillages - historyRecord.TotalVillages,
				Points:            tribe.Points - historyRecord.Points,
				AllPoints:         tribe.AllPoints - historyRecord.AllPoints,
				Rank:              (tribe.Rank - historyRecord.Rank) * -1,
				Dominance:         tribe.Dominance - historyRecord.Dominance,
				CreateDate:        historyRecord.CreateDate,
				OpponentsDefeated: w.calculateODifference(tribe.OpponentsDefeated, historyRecord.OpponentsDefeated),
			})
		}
	}

	return todaysStats
}

func (w *workerUpdateServerData) calculateDailyPlayerStats(players []*models.Player,
	history []*models.PlayerHistory) []*models.DailyPlayerStats {
	var todaysStats []*models.DailyPlayerStats
	searchablePlayers := makePlayersSearchable(players)

	for _, historyRecord := range history {
		if index := searchByID(searchablePlayers, historyRecord.PlayerID); index != -1 {
			player := players[index]
			todaysStats = append(todaysStats, &models.DailyPlayerStats{
				PlayerID:          player.ID,
				Villages:          player.TotalVillages - historyRecord.TotalVillages,
				Points:            player.Points - historyRecord.Points,
				Rank:              (player.Rank - historyRecord.Rank) * -1,
				CreateDate:        historyRecord.CreateDate,
				OpponentsDefeated: w.calculateODifference(player.OpponentsDefeated, historyRecord.OpponentsDefeated),
			})
		}
	}

	return todaysStats
}

func (w *workerUpdateServerData) update() error {
	pod, err := w.dataloader.LoadOD(false)
	if err != nil {
		return errors.Wrap(err, "workerUpdateServerData.update")
	}

	tod, err := w.dataloader.LoadOD(true)
	if err != nil {
		return errors.Wrap(err, "workerUpdateServerData.update")
	}

	villages, err := w.dataloader.LoadVillages()
	if err != nil {
		return errors.Wrap(err, "workerUpdateServerData.update")
	}
	numberOfVillages := len(villages)

	tribes, err := w.loadTribes(tod, countPlayerVillages(villages))
	if err != nil {
		return errors.Wrap(err, "workerUpdateServerData.update")
	}
	numberOfTribes := len(tribes)

	playersResult, err := w.loadPlayers(pod)
	if err != nil {
		return errors.Wrap(err, "workerUpdateServerData.update")
	}

	cfg, err := w.dataloader.GetConfig()
	if err != nil {
		return errors.Wrap(err, "workerUpdateServerData.update")
	}

	buildingCfg, err := w.dataloader.GetBuildingConfig()
	if err != nil {
		return errors.Wrap(err, "workerUpdateServerData.update")
	}

	unitCfg, err := w.dataloader.GetUnitConfig()
	if err != nil {
		return errors.Wrap(err, "workerUpdateServerData.update")
	}

	return w.db.RunInTransaction(context.Background(), func(tx *pg.Tx) error {
		if len(tribes) > 0 {
			ids := make([]int, len(tribes))
			for index, tribe := range tribes {
				ids[index] = tribe.ID
			}

			if _, err := tx.Model(&tribes).
				OnConflict("(id) DO UPDATE").
				Set("name = EXCLUDED.name").
				Set("tag = EXCLUDED.tag").
				Set("total_members = EXCLUDED.total_members").
				Set("total_villages = EXCLUDED.total_villages").
				Set("points = EXCLUDED.points").
				Set("all_points = EXCLUDED.all_points").
				Set("rank = EXCLUDED.rank").
				Set("exists = EXCLUDED.exists").
				Set("dominance = EXCLUDED.dominance").
				Set("deleted_at = null").
				Apply(appendODSetClauses).
				Insert(); err != nil {
				return errors.Wrap(err, "couldn't insert tribes")
			}
			if _, err := tx.Model(&tribes).
				Where("NOT (tribe.id  = ANY (?))", pg.Array(ids)).
				Set("exists = false").
				Set("deleted_at = now()").
				Set("dominance = 0").
				Update(); err != nil && err != pg.ErrNoRows {
				return errors.Wrap(err, "couldn't update non-existent tribes")
			}

			var tribesHistory []*models.TribeHistory
			if err := tx.
				Model(&tribesHistory).
				DistinctOn("tribe_id").
				Column("tribe_history.*").
				Where("tribe.exists = true").
				Order("tribe_id DESC", "create_date DESC").
				Relation("Tribe._").
				Select(); err != nil && err != pg.ErrNoRows {
				return errors.Wrap(err, "couldn't select tribe history records")
			}
			todaysTribeStats := w.calculateTodaysTribeStats(tribes, tribesHistory)
			if len(todaysTribeStats) > 0 {
				if _, err := tx.
					Model(&todaysTribeStats).
					OnConflict("ON CONSTRAINT daily_tribe_stats_tribe_id_create_date_key DO UPDATE").
					Set("members = EXCLUDED.members").
					Set("villages = EXCLUDED.villages").
					Set("points = EXCLUDED.points").
					Set("all_points = EXCLUDED.all_points").
					Set("rank = EXCLUDED.rank").
					Set("dominance = EXCLUDED.dominance").
					Apply(appendODSetClauses).
					Insert(); err != nil {
					return errors.Wrap(err, "couldn't insert today's tribe stats")
				}
			}
		}

		if len(playersResult.deletedPlayers) > 0 {
			if _, err := tx.Model(&models.Player{}).
				Where("player.id = ANY (?)", pg.Array(playersResult.deletedPlayers)).
				Set("exists = false").
				Set("tribe_id = 0").
				Update(); err != nil && err != pg.ErrNoRows {
				return errors.Wrap(err, "couldn't mark players as deleted")
			}
		}

		if playersResult.numberOfPlayers > 0 {
			if _, err := tx.Model(&playersResult.players).
				OnConflict("(id) DO UPDATE").
				Set("name = EXCLUDED.name").
				Set("total_villages = EXCLUDED.total_villages").
				Set("points = EXCLUDED.points").
				Set("rank = EXCLUDED.rank").
				Set("exists = EXCLUDED.exists").
				Set("tribe_id = EXCLUDED.tribe_id").
				Set("daily_growth = EXCLUDED.daily_growth").
				Set("deleted_at = null").
				Apply(appendODSetClauses).
				Insert(); err != nil {
				return errors.Wrap(err, "couldn't insert players")
			}

			var playerHistory []*models.PlayerHistory
			if err := tx.Model(&playerHistory).
				DistinctOn("player_id").
				Column("player_history.*").
				Where("player.exists = true").
				Relation("Player._").
				Order("player_id DESC", "create_date DESC").Select(); err != nil && err != pg.ErrNoRows {
				return errors.Wrap(err, "couldn't select player history records")
			}
			todaysPlayerStats := w.calculateDailyPlayerStats(playersResult.players, playerHistory)
			if len(todaysPlayerStats) > 0 {
				if _, err := tx.
					Model(&todaysPlayerStats).
					OnConflict("ON CONSTRAINT daily_player_stats_player_id_create_date_key DO UPDATE").
					Set("villages = EXCLUDED.villages").
					Set("points = EXCLUDED.points").
					Set("rank = EXCLUDED.rank").
					Apply(appendODSetClauses).
					Insert(); err != nil {
					return errors.Wrap(err, "couldn't insert today's player stats")
				}
			}
		}

		if len(playersResult.playersToServer) > 0 {
			if _, err := tx.Model(&playersResult.playersToServer).OnConflict("DO NOTHING").Insert(); err != nil {
				return errors.Wrap(err, "couldn't associate players with the server")
			}
		}

		if len(villages) > 0 {
			if _, err := tx.Model(&villages).
				OnConflict("(id) DO UPDATE").
				Set("name = EXCLUDED.name").
				Set("points = EXCLUDED.points").
				Set("x = EXCLUDED.x").
				Set("y = EXCLUDED.y").
				Set("bonus = EXCLUDED.bonus").
				Set("player_id = EXCLUDED.player_id").
				Insert(); err != nil {
				return errors.Wrap(err, "couldn't insert villages")
			}
		}

		if _, err := tx.Model(w.server).
			Set("data_updated_at = ?", time.Now()).
			Set("unit_config = ?", unitCfg).
			Set("building_config = ?", buildingCfg).
			Set("config = ?", cfg).
			Set("number_of_players = ?", playersResult.numberOfPlayers).
			Set("number_of_tribes = ?", numberOfTribes).
			Set("number_of_villages = ?", numberOfVillages).
			Returning("*").
			WherePK().
			Update(); err != nil {
			return errors.Wrap(err, "couldn't update server")
		}
		return nil
	})
}

func appendODSetClauses(q *orm.Query) (*orm.Query, error) {
	return q.Set("rank_att = EXCLUDED.rank_att").
			Set("score_att = EXCLUDED.score_att").
			Set("rank_def = EXCLUDED.rank_def").
			Set("score_def = EXCLUDED.score_def").
			Set("rank_sup = EXCLUDED.rank_sup").
			Set("score_sup = EXCLUDED.score_sup").
			Set("rank_total = EXCLUDED.rank_total").
			Set("score_total = EXCLUDED.score_total"),
		nil
}